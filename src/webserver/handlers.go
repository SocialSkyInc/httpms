package webserver

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ironsmile/httpms/src/library"
)

// Handler wrapper used for basic authenticate. Its only job is to do the
// authentication and then pass the work to the Handler it wraps around
type BasicAuthHandler struct {
	wrapped  http.Handler // The actual handler that does the APP Logic job
	username string       // Username to be used for basic authenticate
	password string       // Password to be used for basic authenticate
}

// Implements the http.Handler interface and does the actual basic authenticate
// check for every request
func (hl BasicAuthHandler) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	auth, err := req.Header["Authorization"]

	if err == false || len(auth) != 1 || hl.authenticate(auth[0]) == false {
		InternalErrorOnErrorHandler(writer, req, hl.challengeAuthentication)
		return
	}

	hl.wrapped.ServeHTTP(writer, req)
}

// Sends 401 and authentication challenge in the writer
func (hl BasicAuthHandler) challengeAuthentication(writer http.ResponseWriter,
	req *http.Request) error {
	tmpl, err := getTemplate("unauthorized.html")

	if err != nil {
		return err
	}

	writer.Header().Set("WWW-Authenticate", `Basic realm="HTTPMS"`)
	writer.WriteHeader(http.StatusUnauthorized)

	err = tmpl.Execute(writer, nil)

	if err != nil {
		return err
	}

	return nil
}

// Compares the authentication header with the stored user and passwords
// and returns true if they pass.
func (hl BasicAuthHandler) authenticate(auth string) bool {

	s := strings.SplitN(auth, " ", 2)

	if len(s) != 2 || s[0] != "Basic" {
		return false
	}

	b, err := base64.StdEncoding.DecodeString(s[1])

	if err != nil {
		return false
	}

	pair := strings.SplitN(string(b), ":", 2)

	if len(pair) != 2 {
		return false
	}

	return pair[0] == hl.username && pair[1] == hl.password
}

type FileHandler struct {
	library library.Library
}

// This method is required by the http.Handler's interface
func (fh FileHandler) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	InternalErrorOnErrorHandler(writer, req, fh.find)
}

// Actually searches through the library for this file and serves it
// if it is found. Returns 404 if not (duh)
// Uses http.FileServer for serving the found files
func (fh FileHandler) find(writer http.ResponseWriter, req *http.Request) error {

	id, err := strconv.Atoi(req.URL.Path)

	if err != nil {
		http.NotFoundHandler().ServeHTTP(writer, req)
	}

	filePath := fh.library.GetFilePath(int64(id))

	_, err = os.Stat(filePath)

	if err != nil {
		http.NotFoundHandler().ServeHTTP(writer, req)
		return nil
	}

	req.URL.Path = "/" + filepath.Base(filePath)
	http.FileServer(http.Dir(filepath.Dir(filePath))).ServeHTTP(writer, req)

	return nil
}

// Handler responsible for search requests. It will use the Library to
// return a list of matched files to the interface.
type SearchHandler struct {
	library library.Library
}

// This method is required by the http.Handler's interface
func (sh SearchHandler) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	InternalErrorOnErrorHandler(writer, req, sh.search)
}

func (sh SearchHandler) search(writer http.ResponseWriter, req *http.Request) error {

	writer.Header().Add("Content-Type", "application/json; charset=utf-8")

	query, err := url.QueryUnescape(req.URL.Path)

	if err != nil {
		return err
	}

	results := sh.library.Search(query)

	if len(results) == 0 {
		writer.Write([]byte("[]"))
		return nil
	}

	marshalled, err := json.Marshal(results)

	if err != nil {
		return err
	}

	writer.Write(marshalled)

	return nil
}

func InternalErrorOnErrorHandler(writer http.ResponseWriter, req *http.Request,
	fnc func(http.ResponseWriter, *http.Request) error) {
	err := fnc(writer, req)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		writer.Write([]byte(err.Error()))
	}
}

// Returns a new SearchHandler for processing search queries. They will be run
// agains the supplied library
func NewSearchHandler(lib library.Library) *SearchHandler {
	sh := new(SearchHandler)
	sh.library = lib
	return sh
}

// Returns a new File handler will will be resposible for serving a file
// from the library identified from its ID.
func NewFileHandler(lib library.Library) *FileHandler {
	fh := new(FileHandler)
	fh.library = lib
	return fh
}
