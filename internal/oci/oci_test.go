package oci

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

func layout(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "blobs", "sha256"), 0700)
	write := func(b []byte, media string) v1.Descriptor {
		d := digest.FromBytes(b)
		if e := os.WriteFile(filepath.Join(root, "blobs", "sha256", d.Encoded()), b, 0600); e != nil {
			t.Fatal(e)
		}
		return v1.Descriptor{MediaType: media, Digest: d, Size: int64(len(b))}
	}
	layer := write([]byte("layer bytes"), v1.MediaTypeImageLayer)
	config := write([]byte(`{"architecture":"amd64","os":"linux"}`), v1.MediaTypeImageConfig)
	manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": v1.MediaTypeImageManifest, "config": config, "layers": []v1.Descriptor{layer}})
	desc := write(manifest, v1.MediaTypeImageManifest)
	index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": v1.MediaTypeImageIndex, "manifests": []v1.Descriptor{desc}})
	_ = os.WriteFile(filepath.Join(root, "index.json"), index, 0600)
	_ = os.WriteFile(filepath.Join(root, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0600)
	return root
}
func TestLayoutDependencyOrderAndCorruptBlob(t *testing.T) {
	root := layout(t)
	image, e := Load(root)
	if e != nil {
		t.Fatal(e)
	}
	if len(image.Blobs) != 2 || len(image.Manifests) != 1 || len(image.Root.Manifest) == 0 {
		t.Fatal("invalid traversal")
	}
	if e = os.WriteFile(image.Blobs[0].Path, []byte("wrong bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Load(root); e == nil {
		t.Fatal("corrupted digest accepted")
	}
}
func TestArchiveTraversalAndSymlinkRejected(t *testing.T) {
	for _, h := range []*tar.Header{{Name: "../escape", Size: 1, Mode: 0600}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}} {
		var b bytes.Buffer
		w := tar.NewWriter(&b)
		if e := w.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Size > 0 {
			_, _ = w.Write([]byte("x"))
		}
		_ = w.Close()
		file := filepath.Join(t.TempDir(), "image.tar")
		_ = os.WriteFile(file, b.Bytes(), 0600)
		if _, e := Load(file); e == nil {
			t.Fatal("unsafe archive accepted")
		}
	}
}
func TestDockerArchiveIsNotOCILayout(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "manifest.json"), []byte("[]"), 0600)
	if _, e := Load(root); e == nil {
		t.Fatal("Docker archive accepted as OCI")
	}
}

func TestAliasedLayoutRootAndEscapingMetadata(t *testing.T) {
	root := layout(t)
	alias := filepath.Join(t.TempDir(), "linked-layout")
	if e := os.Symlink(root, alias); e != nil {
		t.Skip("当前平台不允许创建目录符号链接")
	}
	image, e := Load(alias)
	if e != nil {
		t.Fatal("合法目录别名被拒绝", e)
	}
	image.Cleanup()
	outside := filepath.Join(t.TempDir(), "outside-index.json")
	data, e := os.ReadFile(filepath.Join(root, "index.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(outside, data, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(root, "index.json")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, filepath.Join(root, "index.json")); e != nil {
		t.Fatal(e)
	}
	if image, e = Load(alias); e == nil {
		image.Cleanup()
		t.Fatal("通过目录别名读取越界 metadata")
	}
}
