package apikey

import (
	"path/filepath"
	"testing"
)

func FuzzResolve(f *testing.F) {
	store, err := OpenStore(filepath.Join(f.TempDir(), "fuzz.db"))
	if err != nil {
		f.Fatal(err)
	}
	defer store.Close()

	clearKey, _, err := store.Generate("id1", "owner1", "test-key", []string{"svc"}, 0)
	if err != nil {
		f.Fatal(err)
	}

	f.Add(clearKey)
	f.Add("hk_0000000000000000000000000000000000000000000000000000000000000000")
	f.Add("hk_")
	f.Add("")
	f.Add("invalid-format-key")
	f.Add("hk_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")

	f.Fuzz(func(t *testing.T, key string) {
		_, err := store.Resolve(key)
		_ = err
	})
}

func FuzzParseServices(f *testing.F) {
	f.Add(`["svc1","svc2"]`)
	f.Add(`[]`)
	f.Add(``)
	f.Add(`[""]`)
	f.Add(`null`)

	f.Fuzz(func(t *testing.T, s string) {
		svcs := parseServices(s)
		_ = svcs
	})
}
