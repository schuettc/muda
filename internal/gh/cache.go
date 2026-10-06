package gh

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	tools "github.com/schuettc/tools-common"
)

// Cache filenames depend only on the API path/query, never on an authorization
// header or token. Values are the raw API response bodies, not request headers.
func (c *Client) cachePath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(c.cacheDir, hex.EncodeToString(sum[:])+".json")
}

func (c *Client) cached(key string) ([]byte, bool) {
	b, err := os.ReadFile(c.cachePath(key))
	return b, err == nil
}

func (c *Client) store(key string, data []byte) error {
	if err := os.MkdirAll(c.cacheDir, 0700); err != nil {
		return err
	}
	return tools.WriteFileAtomic(c.cachePath(key), data, 0600)
}
