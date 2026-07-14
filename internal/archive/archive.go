// Package archive gives run assembly a uniform view over the two shapes a
// downloaded GitHub Actions log archive takes on disk: the original .zip
// (as served by the "Download log archive" button, `gh run view --log`'s
// cache, or the /actions/runs/{id}/logs REST endpoint) and a directory you
// already unzipped. Zip detection is by magic bytes, not extension, so a
// file saved as `logs` or `run-42.bin` still opens.
package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one file inside the archive. Paths always use forward slashes
// and are relative to the archive root, regardless of source shape.
type Entry struct {
	Path string
	Size int64
	open func() (io.ReadCloser, error)
}

// Open returns a reader for the entry's content.
func (e Entry) Open() (io.ReadCloser, error) { return e.open() }

// Source is an opened archive. Close releases the underlying zip handle;
// it is a no-op for directory sources.
type Source struct {
	// Label names the archive for report headers: the path as given.
	Label   string
	Entries []Entry
	closer  io.Closer
}

// Close releases OS resources held by the source.
func (s *Source) Close() error {
	if s.closer == nil {
		return nil
	}
	return s.closer.Close()
}

// Open opens path as a log archive: a directory is walked, anything else is
// sniffed for the zip magic. Entries are returned sorted by path so every
// downstream ordering is deterministic.
func Open(p string) (*Source, error) {
	info, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	var src *Source
	if info.IsDir() {
		src, err = openDir(p)
	} else {
		src, err = openZip(p, info.Size())
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(src.Entries, func(i, j int) bool { return src.Entries[i].Path < src.Entries[j].Path })
	return src, nil
}

// zipMagic is the local-file-header signature every non-empty zip starts
// with. Empty zips start with "PK\x05\x06" and hold no logs anyway.
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

func openZip(p string, size int64) (*Source, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(f, head); err != nil || string(head) != string(zipMagic) {
		f.Close()
		return nil, fmt.Errorf("%s is neither a directory nor a zip archive (expected a downloaded Actions log archive)", p)
	}
	zr, err := zip.NewReader(f, size)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("open %s: %w", p, err)
	}
	src := &Source{Label: p, closer: f}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || skip(zf.Name) {
			continue
		}
		zf := zf
		src.Entries = append(src.Entries, Entry{
			Path: path.Clean(strings.ReplaceAll(zf.Name, `\`, "/")),
			Size: int64(zf.UncompressedSize64),
			open: func() (io.ReadCloser, error) { return zf.Open() },
		})
	}
	return src, nil
}

func openDir(root string) (*Source, error) {
	src := &Source{Label: root}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skip(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		abs := p
		src.Entries = append(src.Entries, Entry{
			Path: rel,
			Size: info.Size(),
			open: func() (io.ReadCloser, error) { return os.Open(abs) },
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return src, nil
}

// skip drops archiver litter that macOS and some unzip tools introduce, so
// `__MACOSX/build/1_Set up job.txt` is never mistaken for a real step log.
func skip(p string) bool {
	p = strings.ReplaceAll(p, `\`, "/")
	for _, part := range strings.Split(p, "/") {
		if part == "__MACOSX" || part == ".DS_Store" || strings.HasPrefix(part, "._") {
			return true
		}
	}
	return false
}
