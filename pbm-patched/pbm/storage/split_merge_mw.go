package storage

import (
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

const (
	pbmPartToken = ".pbmpart."
	GB           = 1024 * 1024 * 1024
)

var pbmPartRE = regexp.MustCompile(`\.pbmpart\.\d+$`)

type SplitMergeMiddleware struct {
	s          Storage
	maxObjSize int64 // in bytes
}

func NewSplitMergeMW(s Storage, maxObjSize float64) Storage {
	maxObjSizeB := int64(maxObjSize * GB)
	return &SplitMergeMiddleware{
		s:          s,
		maxObjSize: maxObjSizeB,
	}
}

func (sm *SplitMergeMiddleware) Type() Type {
	return sm.s.Type()
}

type wInfo struct {
	n   int64
	err error
}

// Save intercepts the data stream and splits a large file into multiple pbm parts
// before saving them to the storage.
func (sm *SplitMergeMiddleware) Save(name string, data io.Reader, options ...Option) error {
	fName := name

	wInfoC := make(chan wInfo)
	for {
		pr, pw := io.Pipe()

		go func() {
			n, err := io.CopyN(pw, data, sm.maxObjSize)
			pw.Close()
			wInfoC <- wInfo{n, err}
		}()

		err := sm.s.Save(fName, pr, options...)
		if err != nil {
			return errors.Wrap(err, "save during split-merge mw")
		}

		winfo := <-wInfoC
		if winfo.err != nil {
			if winfo.err == io.EOF && winfo.n != 0 {
				break
			} else if winfo.err == io.EOF && winfo.n == 0 {
				// empty part needs to be deleted, empty base should stay
				if isPartFile(fName) {
					if err := sm.s.Delete(fName); err != nil {
						return errors.Wrap(err, "empty file deletion")
					}
				}
				break
			}
			return errors.Wrap(winfo.err, "write pipeline split-merge mw")
		}

		fName, err = createNextPart(fName)
		if err != nil {
			return errors.Wrap(err, "pbm part name creation")
		}
	}

	return nil
}

// SourceReader merges multiple pbm file parts and returns a single stream.
func (sm *SplitMergeMiddleware) SourceReader(name string) (io.ReadCloser, error) {
	fi, err := sm.fileWithParts(name)
	if err != nil &&
		!errors.Is(err, ErrEmpty) &&
		!errors.Is(err, ErrNotExist) {
		return nil, errors.Wrap(err, "list with parts for mw source reader")
	}
	if len(fi) <= 1 {
		return sm.s.SourceReader(name)
	}

	pr, pw := io.Pipe()

	go func() {
		for _, f := range fi {
			r, err := sm.s.SourceReader(f.Name)
			if err != nil {
				pw.CloseWithError(errors.Wrapf(err, "reading pbm part %s", f.Name))
				return
			}
			if _, err = io.Copy(pw, r); err != nil {
				pw.CloseWithError(errors.Wrapf(err, "copy file stream: %s:", f.Name))
				r.Close()
				return
			}
			if err = r.Close(); err != nil {
				pw.CloseWithError(errors.Wrapf(err, "closing file stream: %s", f.Name))
				return
			}
		}
		pw.Close()
	}()

	return pr, nil
}

// FileStat returns the combined file information (name & total size) for a file
// which is split into multiple pbm parts.
func (sm *SplitMergeMiddleware) FileStat(name string) (FileInfo, error) {
	fi, err := sm.fileWithParts(name)
	if err != nil &&
		!errors.Is(err, ErrEmpty) &&
		!errors.Is(err, ErrNotExist) {
		return FileInfo{}, errors.Wrap(err, "list with parts for mw file stat op")
	}
	if len(fi) <= 1 {
		return sm.s.FileStat(name)
	}

	totalSize := int64(0)
	for _, f := range fi {
		totalSize += f.Size
	}
	res := FileInfo{
		Name: fi[0].Name, // the base part has 0 index
		Size: totalSize,
	}

	return res, nil
}

// List returns a list of FileInfo for all files specified with prefix,
// aggregating information for files split into pbm parts.
func (sm *SplitMergeMiddleware) List(prefix, suffix string) ([]FileInfo, error) {
	var fi []FileInfo
	var err error
	if suffix == ".tmp" {
		fi, err = sm.s.List(prefix, suffix)
	} else {
		// fetch all without suffix, and filter after
		fi, err = sm.s.List(prefix, "")
	}
	if err != nil {
		return nil, errors.Wrap(err, "list files for mw list op")
	}

	baseParts := map[string]int64{}
	for _, f := range fi {
		baseFile := GetBasePart(f.Name)
		if !strings.HasSuffix(baseFile, suffix) {
			continue
		}
		baseParts[baseFile] += f.Size
	}

	res := make([]FileInfo, len(baseParts))
	i := 0
	for f, s := range baseParts {
		res[i] = FileInfo{Name: f, Size: s}
		i++
	}

	return res, nil
}

// Delete handles the deletion of a file, including all of its split parts.
func (sm *SplitMergeMiddleware) Delete(name string) error {
	fi, err := sm.fileWithParts(name)
	if err != nil &&
		!errors.Is(err, ErrEmpty) &&
		!errors.Is(err, ErrNotExist) {
		return errors.Wrap(err, "list with parts for mw delete op")
	}
	if len(fi) <= 1 {
		return sm.s.Delete(name)
	}

	for _, f := range fi {
		if err = sm.s.Delete(f.Name); err != nil {
			return errors.Wrapf(err, "delete file part: %s", f.Name)
		}
	}

	return nil
}

// Copy handles copying a file and all its split parts to a new location.
func (sm *SplitMergeMiddleware) Copy(src, dst string) error {
	fi, err := sm.fileWithParts(src)
	if err != nil &&
		!errors.Is(err, ErrEmpty) &&
		!errors.Is(err, ErrNotExist) {
		return errors.Wrap(err, "list with parts for mw copy op")
	}
	if len(fi) <= 1 {
		return sm.s.Copy(src, dst)
	}

	dstPartName := dst
	for _, f := range fi {
		if f.Name == src {
			// copy base part
			if err = sm.s.Copy(src, dstPartName); err != nil {
				return errors.Wrap(err, "copy base part")
			}
		} else {
			dstPartName, err = createNextPart(dstPartName)
			if err != nil {
				return errors.Wrap(err, "create next part name")
			}
			if err = sm.s.Copy(f.Name, dstPartName); err != nil {
				return errors.Wrapf(err, "copy %s to %s", f.Name, dstPartName)
			}
		}
	}
	return nil
}

func (sm *SplitMergeMiddleware) DownloadStat() DownloadStat {
	return sm.s.DownloadStat()
}

// fileWithParts fetches a list of FileInfo for the base file and all its PBM parts.
// The base part has always 0 index, and all other parts have the array index the
// same as pbm part index.
func (sm *SplitMergeMiddleware) fileWithParts(name string) ([]FileInfo, error) {
	res := []FileInfo{}

	fi, err := sm.s.FileStat(name)
	if err != nil {
		return res, errors.Wrapf(err, "fetching pbm file parts base for %s", name)
	}
	res = append(res, fi)

	nextPart := name
	for {
		nextPart, err = createNextPart(nextPart)
		if err != nil {
			return []FileInfo{}, errors.Wrap(err, "creating next part")
		}
		fi, err = sm.s.FileStat(nextPart)
		if err != nil {
			if err == ErrNotExist || err == ErrEmpty {
				break
			}
			return []FileInfo{}, errors.Wrap(err, "fetching next part")
		}
		res = append(res, fi)
	}

	return res, nil
}

// createNextPart returns file name for the next pbm part.
// Input for the name creation is the last part name: base part or any indexed part.
// For part names PBM uses following naming schema:
// file_name.pbmpart.15, where:
// - file_name is the base file name
// - `.pbmpart.` is token that identifies PBM's multi files schema naming
// - 15 is part index
//
// Example of PBM's multi-files schena on disk:
// collection-14-4294136943066280761.wt <-- base part, it has zero-based index which is omitted
// collection-14-4294136943066280761.wt.pbmpart.1 <-- second part (part index 1)
// collection-14-4294136943066280761.wt.pbmpart.2 <-- third part (part index 2)
// collection-14-4294136943066280761.wt.pbmpart.3 <-- the last part
func createNextPart(fname string) (string, error) {
	if isPartFile(fname) {
		fileParts := strings.Split(fname, ".")

		partID, err := strconv.Atoi(fileParts[len(fileParts)-1])
		if err != nil {
			return "", errors.Wrap(err, "parsing id pbm part")
		}
		partID++

		fNewName := fmt.Sprintf("%s.%d", strings.Join(fileParts[:len(fileParts)-1], "."), partID)
		return fNewName, nil
	} else {
		// creating part name based on base part: e.g. base-file.pbmpart.1
		return fmt.Sprintf("%s%s1", fname, pbmPartToken), nil
	}
}

// GetPartIndex extracts the part index from a pbm part file name.
func GetPartIndex(fname string) (int, error) {
	partID := 0
	if isPartFile(fname) {
		fileParts := strings.Split(fname, ".")

		var err error
		partID, err = strconv.Atoi(fileParts[len(fileParts)-1])
		if err != nil {
			return 0, errors.Wrap(err, "parsing id pbm part")
		}
	}

	return partID, nil
}

// GetBasePart extract base part of the file.
// Base part is file without .pbmpart.xy suffix.
func GetBasePart(fname string) string {
	base := fname

	if pbmPartRE.MatchString(fname) {
		base = strings.Split(fname, pbmPartToken)[0]
	}

	return base
}

func isPartFile(fname string) bool {
	return strings.Contains(fname, pbmPartToken)
}
