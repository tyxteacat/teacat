// Package registry pulls models from the Ollama registry (an OCI registry at
// registry.ollama.ai) or from direct GGUF URLs.
package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tyxteacat/teacat/internal/brand"
	"github.com/tyxteacat/teacat/internal/gguf"
	"github.com/tyxteacat/teacat/internal/store"
)

const defaultRegistry = "registry.ollama.ai"

// uaTransport adds the TeaCat User-Agent to every registry request so the
// project shows up consistently in upstream logs and analytics.
type uaTransport struct{ base http.RoundTripper }

func (t uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("User-Agent", brand.UserAgent())
	return t.base.RoundTrip(clone)
}

func newClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: uaTransport{base: http.DefaultTransport}}
}

type ociManifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        ociDescriptor `json:"config"`
	Layers        []ociLayer   `json:"layers"`
}

type ociDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type ociLayer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// Progress is called during downloads. total is -1 when unknown.
type Progress func(what string, done, total int64)

// Pull downloads a model reference such as "smollm2:135m" into the store.
func Pull(st *store.Store, ref string, progress Progress) (store.Manifest, error) {
	if progress == nil {
		progress = func(string, int64, int64) {}
	}
	host, namespace, name, tag := parseRef(ref)
	client := newClient(30 * time.Minute)

	manifestURL := fmt.Sprintf("https://%s/v2/%s/%s/manifests/%s", host, namespace, name, tag)
	req, _ := http.NewRequest(http.MethodGet, manifestURL, nil)
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json")
	resp, err := client.Do(req)
	if err != nil {
		return store.Manifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return store.Manifest{}, fmt.Errorf("registry: %s returned %s: %s", manifestURL, resp.Status, strings.TrimSpace(string(body)))
	}
	var man ociManifest
	if err := json.NewDecoder(resp.Body).Decode(&man); err != nil {
		return store.Manifest{}, err
	}

	var modelDigest string
	var modelSize int64
	var template, system string
	params := map[string]any{}

	for _, layer := range man.Layers {
		path := st.BlobPath(layer.Digest)
		if err := downloadBlob(client, host, namespace, name, layer.Digest, layer.Size, path, progress); err != nil {
			return store.Manifest{}, err
		}
		switch layer.MediaType {
		case "application/vnd.ollama.image.model":
			modelDigest = layer.Digest
			modelSize = layer.Size
		case "application/vnd.ollama.image.template":
			b, _ := os.ReadFile(path)
			template = string(b)
		case "application/vnd.ollama.image.system":
			b, _ := os.ReadFile(path)
			system = string(b)
		case "application/vnd.ollama.image.params":
			b, _ := os.ReadFile(path)
			params = parseParams(string(b))
		}
	}
	if modelDigest == "" {
		return store.Manifest{}, fmt.Errorf("registry: manifest for %s has no model layer", ref)
	}

	m := store.Manifest{
		Name:       name,
		Tag:        tag,
		GGUF:       st.BlobPath(modelDigest),
		Size:       modelSize,
		Digest:     modelDigest,
		Template:   template,
		System:     system,
		Parameters: params,
		Source:     fmt.Sprintf("%s/%s/%s:%s", host, namespace, name, tag),
		ModifiedAt: time.Now(),
	}
	if qi, err := gguf.ReadQuickInfo(m.GGUF); err == nil {
		m.Arch, m.Params, m.Quant = qi.Arch, qi.Params, qi.Quant
	}
	if err := st.Save(m); err != nil {
		return store.Manifest{}, err
	}
	return m, nil
}

// ImportURL downloads a direct .gguf URL into the store.
func ImportURL(st *store.Store, ref, url string, progress Progress) (store.Manifest, error) {
	if progress == nil {
		progress = func(string, int64, int64) {}
	}
	name, tag := store.SplitRef(ref)
	tmp, err := os.CreateTemp(st.BlobsDir(), "download-*")
	if err != nil {
		return store.Manifest{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	client := newClient(2 * time.Hour)
	resp, err := client.Get(url)
	if err != nil {
		tmp.Close()
		return store.Manifest{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		return store.Manifest{}, fmt.Errorf("download: %s returned %s", url, resp.Status)
	}
	h := sha256.New()
	total := resp.ContentLength
	written, err := copyWithProgress(io.MultiWriter(tmp, h), resp.Body, total, "model", progress)
	if err != nil {
		tmp.Close()
		return store.Manifest{}, err
	}
	if err := tmp.Close(); err != nil {
		return store.Manifest{}, err
	}
	digest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	dest := st.BlobPath(digest)
	if err := os.Rename(tmpName, dest); err != nil {
		b, rerr := os.ReadFile(tmpName)
		if rerr != nil {
			return store.Manifest{}, err
		}
		if werr := os.WriteFile(dest, b, 0o644); werr != nil {
			return store.Manifest{}, werr
		}
	}
	m := store.Manifest{
		Name: name, Tag: tag, GGUF: dest, Size: written, Digest: digest,
		Source: url, ModifiedAt: time.Now(),
	}
	if qi, err := gguf.ReadQuickInfo(dest); err == nil {
		m.Arch, m.Params, m.Quant = qi.Arch, qi.Params, qi.Quant
	}
	return m, st.Save(m)
}

func downloadBlob(client *http.Client, host, ns, name, digest string, size int64, dest string, progress Progress) error {
	if st, err := os.Stat(dest); err == nil && st.Size() == size {
		progress("blob", size, size)
		return nil
	}
	url := fmt.Sprintf("https://%s/v2/%s/%s/blobs/%s", host, ns, name, digest)
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("registry: downloading %s: %s: %s", digest, resp.Status, strings.TrimSpace(string(body)))
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), "blob-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	h := sha256.New()
	written, err := copyWithProgress(io.MultiWriter(tmp, h), resp.Body, size, "blob", progress)
	if err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if size > 0 && written != size {
		return fmt.Errorf("registry: blob %s truncated (%d of %d bytes)", digest, written, size)
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != digest {
		return fmt.Errorf("registry: blob %s checksum mismatch (got %s)", digest, got)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return err
	}
	return nil
}

func copyWithProgress(dst io.Writer, src io.Reader, total int64, what string, progress Progress) (int64, error) {
	buf := make([]byte, 256*1024)
	var written int64
	last := time.Now()
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
			if time.Since(last) > 100*time.Millisecond {
				progress(what, written, total)
				last = time.Now()
			}
		}
		if err == io.EOF {
			progress(what, written, total)
			return written, nil
		}
		if err != nil {
			return written, err
		}
	}
}

func parseRef(ref string) (host, namespace, name, tag string) {
	ref = strings.TrimSpace(ref)
	host = defaultRegistry
	if i := strings.Index(ref, "/"); i > 0 && strings.Contains(ref[:i], ".") {
		host = ref[:i]
		ref = ref[i+1:]
	}
	name, tag = store.SplitRef(ref)
	parts := strings.Split(name, "/")
	namespace = "library"
	if len(parts) > 1 {
		namespace = strings.Join(parts[:len(parts)-1], "/")
		name = parts[len(parts)-1]
	}
	return host, namespace, name, tag
}

func parseParams(s string) map[string]any {
	out := map[string]any{}
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := fields[0]
		val := strings.Join(fields[1:], " ")
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			out[key] = n
			continue
		}
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			out[key] = f
			continue
		}
		out[key] = val
	}
	return out
}
