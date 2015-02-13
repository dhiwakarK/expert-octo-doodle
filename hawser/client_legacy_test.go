package hawser

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPut(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	tmp := tempdir(t)
	defer server.Close()
	defer os.RemoveAll(tmp)

	mux.HandleFunc("/media/objects/oid", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			w.WriteHeader(405)
			return
		}

		by, err := ioutil.ReadAll(r.Body)
		if err != nil {
			t.Errorf("Error reading request body: %s", err)
		}

		r.Body.Close()
		if body := string(by); body != "test" {
			t.Errorf("Unexpected body: %s", body)
		}

		w.WriteHeader(200)
	})

	Config.SetConfig("hawser.url", server.URL+"/media")
	oidPath := filepath.Join(tmp, "oid")
	if err := ioutil.WriteFile(oidPath, []byte("test"), 0744); err != nil {
		t.Fatalf("Unable to write oid file: %s", err)
	}

	if err := callPut(oidPath, "", nil); err != nil {
		t.Errorf("Unexpected error: %s", err)
	}
}

func TestOptions(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	tmp := tempdir(t)
	defer server.Close()
	defer os.RemoveAll(tmp)

	mux.HandleFunc("/media/objects/oid", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "OPTIONS" {
			w.WriteHeader(405)
			return
		}

		w.WriteHeader(200)
	})

	Config.SetConfig("hawser.url", server.URL+"/media")
	oidPath := filepath.Join(tmp, "oid")
	if err := ioutil.WriteFile(oidPath, []byte("test"), 0744); err != nil {
		t.Fatalf("Unable to write oid file: %s", err)
	}

	status, err := callOptions(oidPath)
	if err != nil {
		t.Errorf("Unexpected error: %s", err)
	}

	if status != 200 {
		t.Errorf("unexpected status: %d", status)
	}
}

func TestOptionsWithoutExistingObject(t *testing.T) {
	status, err := callOptions("/this/better/not/work")
	if err == nil {
		t.Errorf("expected an error to be returned")
	}

	if status != 0 {
		t.Errorf("unexpected status: %d", status)
	}
}
