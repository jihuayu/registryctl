package model

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
)

type Identity struct {
	Issuer  string `json:"iss"`
	Group   string `json:"group_id"`
	Subject string `json:"sub"`
	Scope   string `json:"scope"`
	Expires int64  `json:"exp"`
	Machine bool   `json:"machine"`
}

func (i Identity) Key() string {
	sum := sha256.Sum256([]byte(i.Issuer + "\x00" + i.Group + "\x00" + i.Subject))
	return hex.EncodeToString(sum[:])
}
func (i Identity) Has(scope string) bool {
	for _, s := range strings.Fields(i.Scope) {
		if s == scope {
			return true
		}
	}
	return false
}

type Session struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Repository string    `json:"repository"`
	Digest     string    `json:"digest"`
	Size       int64     `json:"size"`
	PartSize   int64     `json:"part_size"`
	PartCount  int       `json:"part_count"`
	MediaType  string    `json:"media_type"`
	State      string    `json:"state"`
	Created    time.Time `json:"created_at"`
	Expires    time.Time `json:"expires_at"`
	LastError  string    `json:"error,omitempty"`
}

type Part struct {
	Number int    `json:"number"`
	Size   int64  `json:"size"`
	ETag   string `json:"etag"`
}
type SignedPart struct {
	Number  int               `json:"number"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Expires time.Time         `json:"expires_at"`
}
type Create struct {
	Repository string `json:"repository"`
	Digest     string `json:"digest"`
	Size       int64  `json:"size"`
	MediaType  string `json:"media_type"`
}
type Grant struct {
	Identity   Identity `json:"identity"`
	Repository string   `json:"repository"`
	Actions    []string `json:"actions"`
}

var ErrMissing = errors.New("not found")

func Random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func Repository(name string) error {
	n, e := reference.WithName(name)
	if e != nil || n.Name() != name || strings.Contains(name, ":") || strings.Contains(name, "@") || len(name) > 255 {
		return errors.New("invalid repository")
	}
	return nil
}
func Digest(value string) error {
	d, e := digest.Parse(value)
	if e != nil || d.Algorithm() != digest.SHA256 {
		return errors.New("only sha256 digests are supported")
	}
	return nil
}
