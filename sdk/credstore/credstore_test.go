package credstore

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestFileStoreConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.json")
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A store per writer stands in for a separate process.
			errs <- File(path).Set(fmt.Sprintf("acc%d", i), &Secret{Kind: "token", Token: "t"})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	m, err := (&fileStore{path: path}).load()
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != n {
		t.Fatalf("stored %d accounts, want %d", len(m), n)
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v (%v)", fi.Mode(), err)
		}
	}
}

func TestKeyringDropsAccessTokensWhenTooBig(t *testing.T) {
	keyring.MockInit()
	orig := keyringSet
	t.Cleanup(func() { keyringSet = orig })
	keyringSet = func(service, user, password string) error {
		if len(password) > 100 {
			return keyring.ErrSetDataTooBig
		}
		return keyring.Set(service, user, password)
	}
	s := &Secret{Kind: "oauth", RefreshToken: "rt", AccessTokens: map[string]AccessToken{"aud": {Token: string(make([]byte, 200))}}}
	if err := Keyring().Set("a", s); err != nil {
		t.Fatal(err)
	}
	got, err := Keyring().Get("a")
	if err != nil || got.RefreshToken != "rt" || len(got.AccessTokens) != 0 {
		t.Fatalf("got %+v (%v)", got, err)
	}
}
