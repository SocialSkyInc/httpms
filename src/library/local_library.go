package library

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/howeyc/fsnotify"
	taglib "github.com/landr0id/go-taglib"

	// Blind import is the way a SQL driver is imported. This is the proposed way
	// from the golang documentation.
	_ "github.com/mattn/go-sqlite3"

	"github.com/ironsmile/httpms/src/config"
	"github.com/ironsmile/httpms/src/helpers"
)

// UnknownLabel will be used in case some media tag is missing. As a consequence
// if there are many files with missing title, artist and album only
// one of them will be saved in the library.
const UnknownLabel = "Unknown"

var (
	// LibraryFastScan is a flag, populated by the -fast-library-scan argument.
	//
	// When `false` (the default), scanning the local library will honour the
	// configuration for occasional sleeping while scanning the file system.
	//
	// When `true`, scanning will be as fast as possible. This may generate high
	// IO load for the duration of the scan.
	LibraryFastScan bool
)

func init() {
	flag.BoolVar(&LibraryFastScan, "fast-library-scan", false, "Do not honour"+
		" the configuration set in 'library_scan'. With this flag,"+
		" scanning will be done as fast as possible. This may be useful when"+
		" running the daemon for the fists time with big libraries.")
}

// LocalLibrary implements the Library interface. Will represent files found on the
// local storage
type LocalLibrary struct {
	// The configuration for how to scan the libraries.
	ScanConfig config.ScanSection

	database string         // The location of the library's database
	paths    []string       // FS locations which contain the library's media files
	db       *sql.DB        // Database handler
	walkWG   sync.WaitGroup // Used to log how much time scanning took
	scanWG   sync.WaitGroup // Used in WaitScan to signal the end of scanning

	// If something needs to be added to the database from the watcher
	// it should use this channel
	mediaChan chan string

	// Used to sync the library's Close with the watcher event handling goroutine
	watchClosedChan chan bool

	// Directory watcher
	watch *fsnotify.Watcher

	// Used to signal when the database writer has stopped
	dbWriterWG sync.WaitGroup

	// Used in the database writer to shortcircuit a idle heartbeat which no one reads
	idleTimer *time.Timer

	// Receiving something on this channel means that the database goroutin is in
	// idle state at the moment
	databaseWriterIdle chan struct{}
}

// Close closes the database connection. It is safe to call it as many times as you want.
func (lib *LocalLibrary) Close() {
	lib.stopWatcher()

	lib.WaitScan()
	lib.walkWG.Wait()

	if lib.mediaChan != nil {
		close(lib.mediaChan)
		lib.mediaChan = nil
	}

	lib.dbWriterWG.Wait()

	if lib.db != nil {
		lib.db.Close()
		lib.db = nil
	}
}

// AddLibraryPath adds a library directory to the list of libraries which will be
// scanned and consequently watched.
func (lib *LocalLibrary) AddLibraryPath(path string) {
	_, err := os.Stat(path)

	if err != nil {
		log.Println(err)
		return
	}

	lib.paths = append(lib.paths, path)
}

// Search searches in the library. Will match against the track's name, artist and album.
func (lib *LocalLibrary) Search(searchTerm string) []SearchResult {
	var output []SearchResult

	searchTerm = fmt.Sprintf("%%%s%%", searchTerm)

	rows, err := lib.db.Query(`
		SELECT
			t.id as track_id,
			t.name as track,
			al.name as album,
			at.name as artist,
			t.number as track_number,
			t.album_id as album_id
		FROM
			tracks as t
				LEFT JOIN albums as al ON al.id = t.album_id
				LEFT JOIN artists as at ON at.id = t.artist_id
		WHERE
			t.name LIKE ? OR
			al.name LIKE ? OR
			at.name LIKE ?
		ORDER BY
			al.name, t.number
	`, searchTerm, searchTerm, searchTerm)

	if err != nil {
		log.Printf("Query not successful: %s\n", err.Error())
		return output
	}

	defer rows.Close()
	for rows.Next() {
		var res SearchResult
		rows.Scan(&res.ID, &res.Title, &res.Album, &res.Artist,
			&res.TrackNumber, &res.AlbumID)
		output = append(output, res)
	}

	return output
}

// GetFilePath returns the filsystem path for a file specified by its ID.
func (lib *LocalLibrary) GetFilePath(ID int64) string {

	smt, err := lib.db.Prepare(`
		SELECT
			fs_path
		FROM
			tracks
		WHERE
			id = ?
	`)

	if err != nil {
		log.Println(err)
		return ""
	}

	defer smt.Close()

	var filePath string
	err = smt.QueryRow(ID).Scan(&filePath)

	if err != nil {
		log.Println(err)
		return ""
	}

	return filePath
}

// GetAlbumFiles satisfies the Library interface
func (lib *LocalLibrary) GetAlbumFiles(albumID int64) []SearchResult {
	var output []SearchResult

	rows, err := lib.db.Query(`
		SELECT
			t.id as track_id,
			t.name as track,
			al.name as album,
			at.name as artist,
			t.number as track_number,
			t.album_id as album_id
		FROM
			tracks as t
				LEFT JOIN albums as al ON al.id = t.album_id
				LEFT JOIN artists as at ON at.id = t.artist_id
		WHERE
			t.album_id = ?
		ORDER BY
			al.name, t.number
	`, albumID)

	if err != nil {
		log.Printf("Query not successful: %s\n", err.Error())
		return output
	}

	defer rows.Close()
	for rows.Next() {
		var res SearchResult
		rows.Scan(&res.ID, &res.Title, &res.Album, &res.Artist,
			&res.TrackNumber, &res.AlbumID)
		output = append(output, res)
	}

	return output
}

// Removes the file from the library. That means finding it in the database and
// removing it from there.
func (lib *LocalLibrary) removeFile(filePath string) {

	fullPath, err := filepath.Abs(filePath)

	if err != nil {
		log.Printf("Error removing %s: %s\n", filePath, err.Error())
		return
	}

	_, err = lib.db.Exec(`
		DELETE FROM tracks
		WHERE fs_path = ?
	`, fullPath)

	if err != nil {
		log.Printf("Error removing %s: %s\n", fullPath, err.Error())
	}
}

// Removes files which belong in this directory from the library.
func (lib *LocalLibrary) removeDirectory(dirPath string) {

	// Adding slash at the end to make sure we are always removing directories
	deleteMatch := fmt.Sprintf("%s/%%", strings.TrimRight(dirPath, "/"))

	_, err := lib.db.Exec(`
		DELETE FROM tracks
		WHERE fs_path LIKE ?
	`, deleteMatch)

	if err != nil {
		log.Printf("Error removing %s: %s\n", dirPath, err.Error())
	}
}

// Reads from the media channel and saves into the database every file
// received.
func (lib *LocalLibrary) databaseWriter(media <-chan string, wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}

	lib.databaseWriterIdle = make(chan struct{})
	defer close(lib.databaseWriterIdle)

	idleTimer := time.NewTimer(10 * time.Millisecond)
	defer func() {
		if !idleTimer.Stop() {
			<-idleTimer.C
		}
	}()

	for {
		select {
		case filename, ok := <-media:
			if !ok {
				return
			}

			if err := lib.AddMedia(filename); err != nil {
				log.Printf("Error adding `%s` to library: %s\n", filename, err)
			}

			if !idleTimer.Stop() {
				<-idleTimer.C
			}

		case <-idleTimer.C:
			lib.sendDBWriterIdleSignal()
		}
		idleTimer.Reset(10 * time.Millisecond)
	}
}

func (lib *LocalLibrary) sendDBWriterIdleSignal() {
	lib.idleTimer.Reset(1 * time.Millisecond)
	select {
	case lib.databaseWriterIdle <- struct{}{}:
		if !lib.idleTimer.Stop() {
			<-lib.idleTimer.C
		}
		return
	case <-lib.idleTimer.C:
		return
	}
}

func (lib *LocalLibrary) waitForDBWriterIdleSignal() {
	<-lib.databaseWriterIdle
}

// Determines if the file will be saved to the database. Only media files which
// jplayer can use are saved.
func (lib *LocalLibrary) isSupportedFormat(path string) bool {
	supportedFormats := []string{
		".mp3",
		".ogg",
		".oga",
		".wav",
		".fla",
		".flac",
		".m4a",
	}

	for _, format := range supportedFormats {
		if !strings.HasSuffix(path, format) {
			continue
		}
		return true
	}
	return false
}

// AddMedia adds a file specified by its filesystem name to the library. Will create the
// needed Artist, Album if neccessery.
func (lib *LocalLibrary) AddMedia(filename string) error {
	if lib.MediaExistsInLibrary(filename) {
		return nil
	}

	_, err := os.Stat(filename)

	if err != nil {
		return err
	}

	file, err := taglib.Read(filename)

	if err != nil {
		return fmt.Errorf("Taglib error for %s: %s", filename, err.Error())
	}

	defer file.Close()

	// log.Printf("New Song:\nArtist: %s\nAlbum: %s\nTitle: %s\nTrack: %d\n",
	// 	file.Artist(), file.Album(), file.Title(), int(file.Track()))

	return lib.insertMediaIntoDatabase(file, filename)
}

// insertMediaIntoDatabase accepts an already parsed media info object, its path.
// The method inserts this media into the library database.
func (lib *LocalLibrary) insertMediaIntoDatabase(file MediaFile, filePath string) error {
	artistID, err := lib.setArtistID(file.Artist())

	if err != nil {
		return err
	}

	albumID, err := lib.setAlbumID(file.Album(), artistID)

	if err != nil {
		return err
	}

	_, err = lib.setTrackID(file.Title(), filePath, int64(file.Track()),
		artistID, albumID)

	if err != nil {
		return err
	}

	return nil
}

// MediaExistsInLibrary checks if the media file with file system path "filename" has
// been added to the library already.
func (lib *LocalLibrary) MediaExistsInLibrary(filename string) bool {
	smt, err := lib.db.Prepare(`
		SELECT
			count(id)
		FROM
			tracks
		WHERE
			fs_path = ?
	`)

	if err != nil {
		log.Println(err)
		return false
	}
	defer smt.Close()

	var count int
	err = smt.QueryRow(filename).Scan(&count)

	if err != nil {
		log.Println(err)
		return false
	}

	return count >= 1
}

// GetArtistID returns the id for this artist. When missing or on error
// returns that error.
func (lib *LocalLibrary) GetArtistID(artist string) (int64, error) {
	smt, err := lib.db.Prepare(`
		SELECT
			id
		FROM
			artists
		WHERE
			name = ?
	`)

	if err != nil {
		return 0, err
	}

	defer smt.Close()

	var id int64
	err = smt.QueryRow(artist).Scan(&id)

	if err != nil {
		return 0, err
	}

	return id, nil
}

// Sets a new ID for this artist if it is new to the library. If not, returns
// its current id.
func (lib *LocalLibrary) setArtistID(artist string) (int64, error) {
	if len(artist) < 1 {
		artist = UnknownLabel
	}

	id, err := lib.GetArtistID(artist)

	if err == nil {
		return id, nil
	}

	stmt, err := lib.db.Prepare(`
			INSERT INTO
				artists (name)
			VALUES
				(?)
	`)

	if err != nil {
		return 0, err
	}

	defer stmt.Close()

	_, err = stmt.Exec(artist)

	if err != nil {
		return 0, err
	}

	return lib.lastInsertID()
}

// GetAlbumID returns the id for this artist's album. When missing or on error
// returns that error.
func (lib *LocalLibrary) GetAlbumID(album string, artistID int64) (int64, error) {
	smt, err := lib.db.Prepare(`
		SELECT
			id
		FROM
			albums
		WHERE
			name = ? AND
			artist_id = ?
	`)

	if err != nil {
		return 0, err
	}

	defer smt.Close()

	var id int64
	err = smt.QueryRow(album, artistID).Scan(&id)

	if err != nil {
		return 0, err
	}

	return id, nil
}

// Sets a new ID for this album if it is new to the library. If not, returns
// its current id. Albums with the same name but by different artists need to have
// separate IDs hence the artistID parameter.
func (lib *LocalLibrary) setAlbumID(album string, artistID int64) (int64, error) {
	if len(album) < 1 {
		album = UnknownLabel
	}

	id, err := lib.GetAlbumID(album, artistID)

	if err == nil {
		return id, nil
	}

	stmt, err := lib.db.Prepare(`
			INSERT INTO
				albums (name, artist_id)
			VALUES
				(?, ?)
	`)

	if err != nil {
		return 0, err
	}

	defer stmt.Close()

	_, err = stmt.Exec(album, artistID)

	if err != nil {
		return 0, err
	}

	return lib.lastInsertID()
}

// GetTrackID returns the id for this track. When missing or on error returns that error.
func (lib *LocalLibrary) GetTrackID(title string,
	artistID, albumID int64) (int64, error) {
	smt, err := lib.db.Prepare(`
		SELECT
			id
		FROM
			tracks
		WHERE
			name = ? AND
			artist_id = ? AND
			album_id = ?
	`)

	if err != nil {
		return 0, err
	}

	defer smt.Close()

	var id int64
	err = smt.QueryRow(title, artistID, albumID).Scan(&id)

	if err != nil {
		return 0, err
	}

	return id, nil
}

// Sets a new ID for this track if it is new to the library. If not, returns
// its current id. Tracks with the same name but by different artists and/or album
// need to have separate IDs hence the artistID and albumID parameters.
// Additionally trackNumber and filesystem path (fsPath) are required. They are
// used when retreiving this particular song for playing.
func (lib *LocalLibrary) setTrackID(title, fsPath string,
	trackNumber, artistID, albumID int64) (int64, error) {

	if len(title) < 1 {
		title = filepath.Base(fsPath)
	}

	id, err := lib.GetTrackID(title, artistID, albumID)

	if err == nil {
		return id, nil
	}

	stmt, err := lib.db.Prepare(`
		INSERT INTO
			tracks (name, album_id, artist_id, fs_path, number)
		VALUES
			(?, ?, ?, ?, ?)
	`)

	if err != nil {
		return 0, err
	}

	defer stmt.Close()

	_, err = stmt.Exec(title, albumID, artistID, fsPath, trackNumber)

	if err != nil {
		return 0, err
	}

	return lib.lastInsertID()
}

// Returns the last ID insert in the database.
func (lib *LocalLibrary) lastInsertID() (int64, error) {
	var id int64

	if lib.db == nil {
		return 0, errors.New("The db connection proprety was nil")
	}

	err := lib.db.QueryRow("SELECT last_insert_rowid();").Scan(&id)

	if err != nil {
		return 0, err
	}

	return id, nil
}

// Initialize should be run once every time a library is created. It checks for the
// sqlite database file and creates one if it is absent. If a file is found
// it does nothing.
func (lib *LocalLibrary) Initialize() error {

	if st, err := os.Stat(lib.database); err == nil && st.Size() > 0 {
		return nil
	}

	sqlSchema, err := lib.readSchema()

	if err != nil {
		return err
	}

	if lib.db == nil {
		return errors.New("Library is not opened. Call its Open method first.")
	}

	queries := strings.Split(sqlSchema, ";")

	for _, query := range queries {
		query = strings.TrimSpace(query)

		if len(query) < 1 {
			continue
		}

		_, err = lib.db.Exec(query)

		if err != nil {
			return err
		}
	}

	return nil
}

// Returns the SQL schema for the library. It is stored in the project root directory
// under sqls/library_schema.sql
func (lib *LocalLibrary) readSchema() (string, error) {
	projRoot, err := helpers.ProjectRoot()

	if err != nil {
		return "", err
	}
	sqlSchemaPath := filepath.Join(projRoot, "sqls", "library_schema.sql")

	outBytes, err := ioutil.ReadFile(sqlSchemaPath)

	if err != nil {
		return "", err
	}

	out := string(outBytes)

	if len(out) < 1 {
		return "", errors.New("SQL schema was empty")
	}

	return out, nil
}

// Truncate Closes the library and removes its database file leaving no traces at all.
func (lib *LocalLibrary) Truncate() error {
	lib.Close()
	return os.Remove(lib.database)
}

// NewLocalLibrary returns a new LocalLibrary which will use for database the file
// specified by databasePath. Also creates the database connection so you does not
// need to worry about that.
func NewLocalLibrary(databasePath string) (*LocalLibrary, error) {
	lib := new(LocalLibrary)
	lib.database = databasePath

	var err error

	lib.db, err = sql.Open("sqlite3", lib.database)

	if err != nil {
		return nil, err
	}

	lib.idleTimer = time.NewTimer(1 * time.Millisecond)
	lib.mediaChan = make(chan string, 100)

	lib.dbWriterWG.Add(1)
	go lib.databaseWriter(lib.mediaChan, &lib.dbWriterWG)

	return lib, nil
}
