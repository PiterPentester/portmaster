package ui

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	resources "github.com/cookieo9/resources-go"

	"github.com/safing/portbase/api"
	"github.com/safing/portbase/log"
	"github.com/safing/portbase/updater"
	"github.com/safing/portmaster/updates"
)

var (
	apps     = make(map[string]*resources.BundleSequence)
	appsLock sync.RWMutex
)

func registerRoutes() error {
	api.RegisterHandleFunc("/assets/{resPath:[a-zA-Z0-9/\\._-]+}", ServeBundle("assets")).Methods("GET", "HEAD")
	api.RegisterHandleFunc("/ui/modules/{moduleName:[a-z]+}", redirAddSlash).Methods("GET", "HEAD")
	api.RegisterHandleFunc("/ui/modules/{moduleName:[a-z]+}/", ServeBundle("")).Methods("GET", "HEAD")
	api.RegisterHandleFunc("/ui/modules/{moduleName:[a-z]+}/{resPath:[a-zA-Z0-9/\\._-]+}", ServeBundle("")).Methods("GET", "HEAD")
	api.RegisterHandleFunc("/", redirectToDefault)

	return nil
}

// ServeBundle serves bundles.
func ServeBundle(defaultModuleName string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		// log.Tracef("ui: request for %s", r.RequestURI)

		vars := api.GetMuxVars(r)
		moduleName, ok := vars["moduleName"]
		if !ok {
			moduleName = defaultModuleName
			if moduleName == "" {
				http.Error(w, "missing module name", http.StatusBadRequest)
				return
			}
		}

		resPath, ok := vars["resPath"]
		if !ok || strings.HasSuffix(resPath, "/") {
			resPath = "index.html"
		}

		appsLock.RLock()
		bundle, ok := apps[moduleName]
		appsLock.RUnlock()
		if ok {
			ServeFileFromBundle(w, r, moduleName, bundle, resPath)
			return
		}

		// get file from update system
		zipFile, err := updates.GetFile(fmt.Sprintf("ui/modules/%s.zip", moduleName))
		if err != nil {
			if err == updater.ErrNotFound {
				log.Tracef("ui: requested module %s does not exist", moduleName)
				http.Error(w, err.Error(), http.StatusNotFound)
			} else {
				log.Tracef("ui: error loading module %s: %s", moduleName, err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}

		// open bundle
		newBundle, err := resources.OpenZip(zipFile.Path())
		if err != nil {
			log.Tracef("ui: error prepping module %s: %s", moduleName, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		bundle = &resources.BundleSequence{newBundle}
		appsLock.Lock()
		apps[moduleName] = bundle
		appsLock.Unlock()

		ServeFileFromBundle(w, r, moduleName, bundle, resPath)
	}
}

// ServeFileFromBundle serves a file from the given bundle.
func ServeFileFromBundle(w http.ResponseWriter, r *http.Request, bundleName string, bundle *resources.BundleSequence, path string) {
	readCloser, err := bundle.Open(path)
	if err != nil {
		if err == resources.ErrNotFound {
			// Check if there is a base index.html file we can serve instead.
			var indexErr error
			path = "index.html"
			readCloser, indexErr = bundle.Open(path)
			if indexErr != nil {
				// If we cannot get an index, continue with handling the original error.
				log.Tracef("ui: requested resource \"%s\" not found in bundle %s: %s", path, bundleName, err)
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
		} else {
			log.Tracef("ui: error opening module %s: %s", bundleName, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// set content type
	_, ok := w.Header()["Content-Type"]
	if !ok {
		contentType := mime.TypeByExtension(filepath.Ext(path))
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
	}

	// TODO: Set content security policy
	// For some reason, this breaks the ui client
	// w.Header().Set("Content-Security-Policy", "default-src 'self'")

	w.WriteHeader(http.StatusOK)
	if r.Method != "HEAD" {
		_, err = io.Copy(w, readCloser)
		if err != nil {
			log.Errorf("ui: failed to serve file: %s", err)
			return
		}
	}

	readCloser.Close()
}

// redirectToDefault redirects the request to the default UI module.
func redirectToDefault(w http.ResponseWriter, r *http.Request) {
	u, err := url.Parse("/ui/modules/portmaster/")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, r.URL.ResolveReference(u).String(), http.StatusTemporaryRedirect)
}

// redirAddSlash redirects the request to the same, but with a trailing slash.
func redirAddSlash(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, r.RequestURI+"/", http.StatusPermanentRedirect)
}
