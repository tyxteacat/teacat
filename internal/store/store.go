// Package store manages downloaded models on disk using content-addressed
// blobs plus JSON manifests, similar to Ollama's layout.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tyxteacat/teacat/internal/gguf"
)

// Manifest describes one installed model.
type Manifest struct {
	Name       string         `json:"name"`
	Tag        string         `json:"tag"`
	GGUF       string         `json:"gguf"`
	Size       int64          `json:"size"`
	Digest     string         `json:"digest"`
	Template   string         `json:"template,omitempty"`
	System     string         `json:"system,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Source     string         `json:"source,omitempty"`
	ModifiedAt time.Time      `json:"modified_at"`
	Arch       string         `json:"arch,omitempty"`
	Params     int64          `json:"params,omitempty"`
	Quant      string         `json:"quant,omitempty"`
}

// Ref is the "name:tag" form used everywhere.
func (m Manifest) Ref() string {
	if m.Tag == "" {
		return m.Name
	}
	return m.Name + ":" + m.Tag
}

// Store is a model directory.
type Store struct {
	Home string
}

// Open returns a store rooted at TEACAT_HOME, or ~/.teacat by default.
func Open() (*Store, error) {
	home := os.Getenv("TEACAT_HOME")
	if home == "" {
		uh, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(uh, ".teacat")
	}
	s := &Store{Home: home}
	if err := os.MkdirAll(s.BlobsDir(), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.ManifestsDir(), 0o755); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) BlobsDir() string     { return filepath.Join(s.Home, "models", "blobs") }
func (s *Store) ManifestsDir() string { return filepath.Join(s.Home, "models", "manifests") }

// BlobPath returns the path for a sha256 digest.
func (s *Store) BlobPath(digest string) string {
	digest = strings.TrimPrefix(digest, "sha256:")
	return filepath.Join(s.BlobsDir(), "sha256-"+digest)
}

func (s *Store) manifestPath(name, tag string) string {
	name = strings.TrimPrefix(filepath.ToSlash(name), "/")
	return filepath.Join(s.ManifestsDir(), filepath.FromSlash(name), tag+".json")
}

// SplitRef splits "name:tag" into its parts, defaulting to tag "latest".
func SplitRef(ref string) (string, string) {
	ref = strings.TrimSpace(ref)
	if i := strings.LastIndex(ref, ":"); i > 0 && !strings.Contains(ref[i:], "/") {
		return ref[:i], ref[i+1:]
	}
	return ref, "latest"
}

// Save writes a manifest to disk.
func (s *Store) Save(m Manifest) error {
	if m.ModifiedAt.IsZero() {
		m.ModifiedAt = time.Now()
	}
	p := s.manifestPath(m.Name, m.Tag)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// Get looks up a model by reference.
func (s *Store) Get(ref string) (Manifest, error) {
	name, tag := SplitRef(ref)
	p := s.manifestPath(name, tag)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, fmt.Errorf("model %q not found (try: teacat pull %s)", ref, ref)
		}
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// List returns all installed models sorted by name.
func (s *Store) List() ([]Manifest, error) {
	var out []Manifest
	root := s.ManifestsDir()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var m Manifest
		if json.Unmarshal(b, &m) == nil {
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref() < out[j].Ref() })
	return out, nil
}

// Remove deletes a model manifest and any blobs no other model references.
func (s *Store) Remove(ref string) error {
	m, err := s.Get(ref)
	if err != nil {
		return err
	}
	if err := os.Remove(s.manifestPath(m.Name, m.Tag)); err != nil {
		return err
	}
	// Clean up the blob if nothing else uses it.
	models, _ := s.List()
	for _, other := range models {
		if other.Digest == m.Digest {
			return nil
		}
	}
	if m.Digest != "" {
		_ = os.Remove(s.BlobPath(m.Digest))
	}
	return nil
}

// ImportFile copies a local GGUF file into the blob store and registers it.
func (s *Store) ImportFile(name, tag, path, source string) (Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Manifest{}, err
	}
	h := sha256.New()
	tmp, err := os.CreateTemp(s.BlobsDir(), "import-*")
	if err != nil {
		return Manifest{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(io.MultiWriter(tmp, h), f); err != nil {
		tmp.Close()
		return Manifest{}, err
	}
	if err := tmp.Close(); err != nil {
		return Manifest{}, err
	}
	digest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	dest := s.BlobPath(digest)
	if err := os.Rename(tmpName, dest); err != nil {
		// Cross-device rename fallback.
		if err := copyFile(tmpName, dest); err != nil {
			return Manifest{}, err
		}
	}
	m := Manifest{
		Name:       name,
		Tag:        tag,
		GGUF:       dest,
		Size:       st.Size(),
		Digest:     digest,
		Source:     source,
		ModifiedAt: time.Now(),
	}
	if qi, err := gguf.ReadQuickInfo(dest); err == nil {
		m.Arch, m.Params, m.Quant = qi.Arch, qi.Params, qi.Quant
	}
	return m, s.Save(m)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
