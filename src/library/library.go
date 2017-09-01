// Package library deals with the actual media library. It is creates the Library type.
//
// Every media receives an ID in the library. The main thing a search result returns
// is the tracks' IDs. They are used to get the media, again using the Library. That
// way the real location of the file is never revealed to the interface.
package library

// SearchResult contains a result for a search term. Contains all the neccessery
// information to uniquely identify a media in the library.
type SearchResult struct {

	// ID in the library for a media file
	ID int64 `json:"id"`

	// Meta info: Artist
	Artist string `json:"artist"`

	// Meta info: Album ID
	AlbumID int64 `json:"album_id"`

	// Meta info: Album for music
	Album string `json:"album"`

	// Meta info: the title of this media file
	Title string `json:"title"`

	// Meta info: track number for music
	TrackNumber int64 `json:"track"`
}

// Artist represents an artist from the database
type Artist struct {
	ID   int64  `json:"artist_id"`
	Name string `json:"artist"`
}

// Album represents an album from the database
type Album struct {
	ID     int64  `json:"album_id"`
	Name   string `json:"album"`
	Artist string `json:"artist"`
}

// Library represents the media library which is played using the HTTPMS.
// It is responsible for scaning the library directories, watching for new files,
// actually searching for a media by a search term and finding the exact file path
// in the file system for a media.
type Library interface {

	// Adds a new path to the library paths. If it hasn't been scanned yet a new scan
	// will be started.
	AddLibraryPath(string)

	// Search the library using a search string. It will match against Artist, Album
	// and Title. Will OR the results. So it is "return anything which Artist matches or
	// Album matches or Title matches"
	Search(string) []SearchResult

	// BrowseArtists makes it possible to browse through the library artists page by page.
	// Returns a list of artists for particular page and the number of all artists in the
	// library.
	BrowseArtists(page, perPage uint) ([]Artist, int)

	// BrowseAlbums makes it possible to browse through the library albums page by page.
	// Returns a list of albums for particular page and the number of all albums in the
	// library.
	BrowseAlbums(page, perPage uint) ([]Album, int)

	// Returns the real filesystem path. Requires the media ID.
	GetFilePath(int64) string

	// Returns search result will all the files of this album
	GetAlbumFiles(int64) []SearchResult

	// Starts a full library scan. Will scan all paths if
	// they are not scanned already.
	Scan()

	// Adds this media (file) to the library
	AddMedia(string) error

	// Makes sure the library is initialied. This method will be called once on
	// every start of the httpms
	Initialize() error

	// Makes the library forget everything. Also Closes the library.
	Truncate() error

	// Frees all resources this library object is using.
	// Any operations (except Truncate) on closed library will result in panic.
	Close()
}
