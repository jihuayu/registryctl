package oci

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jihuayu/registryctl/internal/model"
	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

type Blob struct {
	Descriptor v1.Descriptor
	Path       string
	Manifest   []byte
}
type Image struct {
	Blobs     []Blob
	Manifests []Blob
	Root      Blob
	Cleanup   func()
}

func Load(source string) (*Image, error) {
	info, e := os.Stat(source)
	if e != nil {
		return nil, e
	}
	root := source
	cleanup := func() {}
	if !info.IsDir() {
		root, e = os.MkdirTemp("", "registryctl-oci-")
		if e != nil {
			return nil, e
		}
		cleanup = func() { _ = os.RemoveAll(root) }
		if e = extract(source, root); e != nil {
			cleanup()
			return nil, e
		}
	}
	image := &Image{Cleanup: cleanup}
	if e = load(root, image); e != nil {
		cleanup()
		return nil, e
	}
	return image, nil
}
func extract(source, root string) error {
	f, e := os.Open(source)
	if e != nil {
		return e
	}
	defer f.Close()
	var reader io.Reader = f
	var magic [2]byte
	_, e = f.Read(magic[:])
	if e != nil {
		return e
	}
	_, _ = f.Seek(0, 0)
	if magic == [2]byte{0x1f, 0x8b} {
		g, e := gzip.NewReader(f)
		if e != nil {
			return e
		}
		defer g.Close()
		reader = g
	}
	t := tar.NewReader(reader)
	var count int
	var total int64
	for {
		h, e := t.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		count++
		total += h.Size
		if count > 20000 || total > 1<<40 || h.Size < 0 {
			return errors.New("archive exceeds limits")
		}
		clean := filepath.Clean(filepath.FromSlash(h.Name))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || strings.Contains(clean, ":") {
			return errors.New("unsafe archive path")
		}
		target := filepath.Join(root, clean)
		switch h.Typeflag {
		case tar.TypeDir:
			if e = os.MkdirAll(target, 0700); e != nil {
				return e
			}
		case tar.TypeReg, tar.TypeRegA:
			if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				return e
			}
			out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				return e
			}
			_, e = io.CopyN(out, t, h.Size)
			closeErr := out.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return errors.New("archive links and special files are rejected")
		}
	}
}
func safeFile(root, relative string) (string, error) {
	p := filepath.Join(root, relative)
	resolved, e := filepath.EvalSymlinks(p)
	if e != nil {
		return "", e
	}
	canonicalRoot, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	abs, e := filepath.Abs(canonicalRoot)
	if e != nil {
		return "", e
	}
	resolved, e = filepath.Abs(resolved)
	if e != nil {
		return "", e
	}
	rel, e := filepath.Rel(abs, resolved)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("OCI path leaves layout")
	}
	return resolved, nil
}
func readMetadata(root, rel string) ([]byte, error) {
	p, e := safeFile(root, rel)
	if e != nil {
		return nil, e
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if len(b) > 1<<20 {
		return nil, errors.New("OCI metadata too large")
	}
	return b, e
}
func load(root string, image *Image) error {
	layout, e := readMetadata(root, "oci-layout")
	if e != nil {
		return errors.New("需要 OCI layout 或 OCI archive，Docker archive 不受支持")
	}
	var l struct {
		Version string `json:"imageLayoutVersion"`
	}
	if json.Unmarshal(layout, &l) != nil || l.Version != "1.0.0" {
		return errors.New("unsupported OCI layout")
	}
	b, e := readMetadata(root, "index.json")
	if e != nil {
		return e
	}
	var index v1.Index
	if json.Unmarshal(b, &index) != nil || index.SchemaVersion != 2 || len(index.Manifests) == 0 {
		return errors.New("invalid OCI index")
	}
	seen := map[string]bool{}
	visiting := map[string]bool{}
	var walk func(v1.Descriptor, int) (Blob, error)
	walk = func(d v1.Descriptor, depth int) (Blob, error) {
		if depth > 64 || len(seen) > 10000 || model.Digest(d.Digest.String()) != nil || d.Size < 0 || len(d.URLs) > 0 {
			return Blob{}, errors.New("invalid OCI descriptor")
		}
		if visiting[d.Digest.String()] {
			return Blob{}, errors.New("cyclic OCI index")
		}
		path, e := safeFile(root, filepath.Join("blobs", "sha256", d.Digest.Encoded()))
		if e != nil {
			return Blob{}, e
		}
		file, e := os.Open(path)
		if e != nil {
			return Blob{}, e
		}
		h := sha256.New()
		size, e := io.Copy(h, file)
		_ = file.Close()
		if e != nil {
			return Blob{}, e
		}
		if size != d.Size || hex.EncodeToString(h.Sum(nil)) != d.Digest.Encoded() {
			return Blob{}, errors.New("OCI blob digest or size mismatch")
		}
		blob := Blob{Descriptor: d, Path: path}
		manifest := d.MediaType == v1.MediaTypeImageManifest || d.MediaType == v1.MediaTypeImageIndex || d.MediaType == "application/vnd.docker.distribution.manifest.v2+json" || d.MediaType == "application/vnd.docker.distribution.manifest.list.v2+json"
		if seen[d.Digest.String()] {
			return blob, nil
		}
		seen[d.Digest.String()] = true
		if !manifest {
			image.Blobs = append(image.Blobs, blob)
			return blob, nil
		}
		visiting[d.Digest.String()] = true
		metadata, e := readMetadata(root, filepath.Join("blobs", "sha256", d.Digest.Encoded()))
		if e != nil {
			return Blob{}, e
		}
		blob.Manifest = metadata
		var node struct {
			SchemaVersion int             `json:"schemaVersion"`
			Config        v1.Descriptor   `json:"config"`
			Layers        []v1.Descriptor `json:"layers"`
			Manifests     []v1.Descriptor `json:"manifests"`
		}
		if json.Unmarshal(metadata, &node) != nil || node.SchemaVersion != 2 {
			return Blob{}, errors.New("invalid OCI manifest")
		}
		children := node.Manifests
		if node.Config.Digest != "" {
			children = append(children, node.Config)
			children = append(children, node.Layers...)
		}
		for _, child := range children {
			if _, e = walk(child, depth+1); e != nil {
				return Blob{}, e
			}
		}
		delete(visiting, d.Digest.String())
		image.Manifests = append(image.Manifests, blob)
		return blob, nil
	}
	if len(index.Manifests) == 1 {
		image.Root, e = walk(index.Manifests[0], 0)
		if e != nil {
			return e
		}
		if len(image.Root.Manifest) == 0 {
			return errors.New("root descriptor must be image manifest or index")
		}
	} else {
		for _, d := range index.Manifests {
			if _, e = walk(d, 0); e != nil {
				return e
			}
		}
		sum := sha256.Sum256(b)
		image.Root = Blob{Descriptor: v1.Descriptor{MediaType: v1.MediaTypeImageIndex, Size: int64(len(b)), Digest: digest.Digest("sha256:" + fmt.Sprintf("%x", sum))}, Manifest: b}
		image.Manifests = append(image.Manifests, image.Root)
	}
	return nil
}
