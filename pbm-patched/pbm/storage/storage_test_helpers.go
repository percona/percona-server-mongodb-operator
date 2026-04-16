package storage

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"path"
	"strings"
	"testing"
)

func RunStorageBaseTests(t *testing.T, stg Storage, stgType Type) {
	t.Helper()

	t.Run("Type", func(t *testing.T) {
		if stg.Type() != stgType {
			t.Errorf("expected storage type %s, got %s", stgType, stg.Type())
		}
	})

	t.Run("Save and FileStat", func(t *testing.T) {
		name := "test.txt"
		content := "content"

		err := stg.Save(name, strings.NewReader(content), Size(int64(len(content))))
		if err != nil {
			t.Fatalf("Save failed: %s", err)
		}

		f, err := stg.FileStat(name)
		if err != nil {
			t.Errorf("FileStat failed: %s", err)
		}

		if f.Size != int64(len(content)) {
			t.Errorf("expected size %d, got %d", len(content), f.Size)
		}
	})

	t.Run("List", func(t *testing.T) {
		d := "root" + randomSuffix()
		filesToSave := []struct {
			name    string
			content string
		}{
			{path.Join(d, "file1.txt"), "content1"},
			{path.Join(d, "file2.log"), "content1"},
			{path.Join(d, "dir/file3.txt"), "content3"},
		}

		for _, f := range filesToSave {
			if err := stg.Save(f.name, strings.NewReader(f.content), Size(int64(len(f.content)))); err != nil {
				t.Fatalf("Save failed: %s", err)
			}
		}

		files, err := stg.List(d, ".txt")
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}

		// expect "file1.txt", "dir/file3.txt" and "test.txt" from previous test to be returned
		if len(files) != 2 {
			t.Errorf("expected 3 .txt files, got %d", len(files))
		}

		files, err = stg.List(path.Join(d, "dir"), ".txt")
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}

		// expect "dir/file3.txt" to be returned
		if len(files) != 1 {
			t.Errorf("expected 1 .txt files, got %d", len(files))
		}
	})

	t.Run("Delete", func(t *testing.T) {
		name := "delete.txt"
		content := "content"

		if err := stg.Save(name, strings.NewReader(content), Size(int64(len(content)))); err != nil {
			t.Fatalf("Save failed: %s", err)
		}

		err := stg.Delete(name)
		if err != nil {
			t.Fatalf("Delete failed: %s", err)
		}

		f, err := stg.FileStat(name)
		if err == nil {
			t.Errorf("expected error for deleted file, got nil")
		}

		if f != (FileInfo{}) {
			t.Errorf("expected %v, got %v", FileInfo{}, f)
		}
	})

	t.Run("Copy", func(t *testing.T) {
		src := "copysrc.txt"
		dst := "copydst.txt"
		content := "copy content"

		if err := stg.Save(src, strings.NewReader(content), Size(int64(len(content)))); err != nil {
			t.Fatalf("Save failed: %s", err)
		}

		if err := stg.Copy(src, dst); err != nil {
			t.Fatalf("Copy failed: %s", err)
		}

		f, err := stg.FileStat(dst)
		if err != nil {
			t.Errorf("FileStat failed: %s", err)
		}

		if f.Size != int64(len(content)) {
			t.Errorf("expected size %d, got %d", len(content), f.Size)
		}
	})

	t.Run("SourceReader", func(t *testing.T) {
		name := "reader.txt"
		content := "source reader content"

		if err := stg.Save(name, strings.NewReader(content), Size(int64(len(content)))); err != nil {
			t.Fatalf("Save failed: %s", err)
		}

		reader, err := stg.SourceReader(name)
		if err != nil {
			t.Fatalf("SourceReader failed: %s", err)
		}

		defer func() {
			err := reader.Close()
			if err != nil {
				t.Fatalf("close failed: %s", err)
			}
		}()

		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll failed: %s", err)
		}

		if string(data) != content {
			t.Errorf("expected content %s, got %s", content, string(data))
		}
	})
}

// RunStorageAPITests contains corner cases for storage API.
func RunStorageAPITests(t *testing.T, stg Storage) {
	t.Helper()

	// remove MW
	stg = stg.(*SplitMergeMiddleware).s

	t.Run("storage api", func(t *testing.T) {
		t.Run("Save", func(t *testing.T) {
			t.Run("create empty file", func(t *testing.T) {
				name := "empty" + randomSuffix()

				err := stg.Save(name, strings.NewReader(""))
				if err != nil {
					t.Fatalf("Save failed: %s", err)
				}

				_, err = stg.FileStat(name)
				if err != ErrEmpty {
					t.Errorf("FileStat failed: want=%v, got=%v", ErrEmpty, err)
				}
			})
		})

		t.Run("SourceReader", func(t *testing.T) {
			t.Run("file doesn't exist", func(t *testing.T) {
				name := "doesnt_exist" + randomSuffix()

				_, err := stg.SourceReader(name)
				if !errors.Is(err, ErrNotExist) {
					t.Fatalf("error reported while invoking SourceReader on non-existing file: %v", err)
				}
			})

			t.Run("empty file", func(t *testing.T) {
				fName := "empty" + randomSuffix()
				err := stg.Save(fName, strings.NewReader(""))
				if err != nil {
					t.Fatalf("Save failed: %s", err)
				}

				_, err = stg.SourceReader(fName)
				if !errors.Is(err, ErrEmpty) {
					t.Fatalf("error reported while invoking SourceReader on empty file: %v", err)
				}
			})
		})

		t.Run("Delete", func(t *testing.T) {
			t.Run("file doesn't exist", func(t *testing.T) {
				name := "doesnt_exist" + randomSuffix()

				err := stg.Delete(name)

				wantErr := ErrNotExist
				if err != wantErr {
					t.Fatalf("error reported while invoking Delete on non-existing file: want=%v, got=%v", wantErr, err)
				}
			})

			t.Run("empty file", func(t *testing.T) {
				fName := "empty" + randomSuffix()
				err := stg.Save(fName, strings.NewReader(""))
				if err != nil {
					t.Fatalf("Save failed: %s", err)
				}

				err = stg.Delete(fName)
				if err != nil {
					t.Fatalf("error reported while invoking Delete on empty file: %v", err)
				}
			})
		})

		t.Run("FileStat", func(t *testing.T) {
			t.Run("file doesn't exist", func(t *testing.T) {
				name := "doesnt_exist" + randomSuffix()

				_, err := stg.FileStat(name)
				if err != ErrNotExist {
					t.Fatalf("wrong error reported: want=%v, got=%v", ErrNotExist, err)
				}
			})

			t.Run("empty file", func(t *testing.T) {
				fName := "empty" + randomSuffix()
				err := stg.Save(fName, strings.NewReader(""))
				if err != nil {
					t.Fatalf("Save failed: %s", err)
				}

				f, err := stg.FileStat(fName)
				if err != ErrEmpty {
					t.Fatalf("wrong error reported: want=%v, got=%v", ErrEmpty, err)
				}
				if f.Name != fName && f.Size != 0 {
					t.Fatalf("wrong file info: want name=%s, size=0, got=%+v", fName, f)
				}
			})
		})

		t.Run("Copy", func(t *testing.T) {
			t.Run("file doesn't exist", func(t *testing.T) {
				name := "doesnt_exist" + randomSuffix()
				dstName := "dst/" + name

				err := stg.Copy(name, dstName)
				if !errors.Is(err, ErrNotExist) {
					t.Fatalf("error reported while invoking Copy on non-existing file: %v", err)
				}
			})

			t.Run("empty file", func(t *testing.T) {
				name := "empty" + randomSuffix()
				dstName := "dst/" + name
				err := stg.Save(name, strings.NewReader(""))
				if err != nil {
					t.Fatalf("Save failed: %s", err)
				}

				err = stg.Copy(name, dstName)
				if err != nil {
					t.Fatalf("error while copying: %v", err)
				}
				_, err = stg.FileStat(dstName)
				if err != ErrEmpty {
					t.Fatalf("wrong error reported: want=%v, got=%v", ErrEmpty, err)
				}
			})
		})
	})
}

func RunSplitMergeMWTests(t *testing.T, stg Storage) {
	t.Helper()

	t.Run("Save with split-merge middleware", func(t *testing.T) {
		testCases := []struct {
			desc      string
			partSize  int64
			fileSize  int64
			file      string
			wantParts int
		}{
			{
				desc:      "basic use case for splitting files",
				partSize:  10 * 1024,
				fileSize:  23 * 1024,
				wantParts: 3,
			},
			{
				desc:      "splitting 100s of files",
				partSize:  1 * 1024,
				fileSize:  220*1024 + 555,
				wantParts: 221,
			},
			{
				desc:      "splitting 1000s of files",
				partSize:  1 * 512,
				fileSize:  1100*1024 + 1,
				wantParts: 2201,
			},
			{
				desc:      "file of the same size as part",
				partSize:  5 * 1024 * 1024,
				fileSize:  5 * 1024 * 1024,
				wantParts: 1,
			},
			{
				desc:      "file size is a multiple of part",
				partSize:  12 * 1024 * 1024,
				fileSize:  48 * 1024 * 1024,
				wantParts: 4,
			},
			{
				desc:      "single file little bit bigger than part",
				partSize:  15 * 1024 * 1024,
				fileSize:  15*1024*1024 + 2,
				wantParts: 2,
			},
			{
				desc:      "single file that's a bit smaller than part",
				partSize:  7 * 1024 * 1024,
				fileSize:  7*1024*1024 - 1,
				wantParts: 1,
			},
			{
				desc:      "lots of parts and one smaller part",
				partSize:  7 * 1024,
				fileSize:  490*1024 + 1,
				wantParts: 71,
			},
			{
				desc:      "1 byte file",
				partSize:  14 * 1024,
				fileSize:  1,
				wantParts: 1,
			},
			{
				desc:      "file in sub dir",
				partSize:  1024,
				fileSize:  3*1024 + 1,
				file:      "sub/file_in_sub",
				wantParts: 4,
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				mw := stg.(*SplitMergeMiddleware)
				mw.maxObjSize = tC.partSize

				fName := "test_split" + randomSuffix()
				if tC.file != "" {
					fName = tC.file
				}
				fContent := make([]byte, tC.fileSize)
				r := bytes.NewReader(fContent)

				err := mw.Save(fName, r)
				if err != nil {
					t.Fatalf("error while saving file: %v", err)
				}

				fi, err := mw.fileWithParts(fName)
				if err != nil {
					t.Fatalf("error while getting parts: %v", err)
				}
				if len(fi) != tC.wantParts {
					t.Fatalf("wrong number of splitted files: want=%d, got=%d", tC.wantParts, len(fi))
				}

				wantSizes := CalcPartSizes(tC.partSize, tC.fileSize)
				for i := range len(fi) {
					if wantSizes[i] != fi[i].Size {
						t.Fatalf("wrong file size for file: %s: want=%d, got=%d", fi[i].Name, wantSizes[i], fi[i].Size)
					}
				}
			})
		}

		t.Run("empty file", func(t *testing.T) {
			mw := stg.(*SplitMergeMiddleware)
			mw.maxObjSize = 1024
			name := "empty" + randomSuffix()

			err := mw.Save(name, strings.NewReader(""))
			if err != nil {
				t.Fatalf("Save failed: %s", err)
			}

			_, err = mw.FileStat(name)
			if err != ErrEmpty {
				t.Errorf("FileStat failed: want=%v, got=%v", ErrEmpty, err)
			}
		})
	})

	t.Run("SourceReader with split-merge middleware", func(t *testing.T) {
		testCases := []struct {
			desc           string
			partSize       int64
			mergedFileSize int64
			file           string
		}{
			{
				desc:           "basic use case for merging parts",
				partSize:       1024,
				mergedFileSize: 11 * 1024,
			},
			{
				desc:           "merging 100s of parts",
				partSize:       199,
				mergedFileSize: 300*199 + 444,
			},
			{
				desc:           "merging 1000s of parts",
				partSize:       57,
				mergedFileSize: 57*1023 + 15,
			},
			{
				desc:           "single part file smaller than part size",
				partSize:       5 * 1024,
				mergedFileSize: 4*1024 + 123,
			},
			{
				desc:           "single part file, the same size as part size",
				partSize:       2 * 1024 * 1024,
				mergedFileSize: 2 * 1024 * 1024,
			},
			{
				desc:           "merged file size is multiple of part size",
				partSize:       10 * 1024 * 1024,
				mergedFileSize: 5 * 10 * 1024 * 1024,
			},
			{
				desc:           "merged file size is for single byte bigger than part",
				partSize:       50 * 1024,
				mergedFileSize: 50*1024 + 1,
			},
			{
				desc:           "1 byte file",
				partSize:       20 * 1024,
				mergedFileSize: 1,
			},
			{
				desc:           "file within dir",
				partSize:       1024,
				mergedFileSize: 4 * 1024,
				file:           "sub/test_merge_in_sub",
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				mw := stg.(*SplitMergeMiddleware)
				mw.maxObjSize = tC.partSize

				fName := "test_merge" + randomSuffix()
				if tC.file != "" {
					fName = tC.file + randomSuffix()
				}
				// create test parts
				srcContent := make([]byte, tC.mergedFileSize)
				r := bytes.NewReader(srcContent)
				err := mw.Save(fName, r)
				if err != nil {
					t.Fatalf("error while creating test parts: %v", err)
				}

				rc, err := mw.SourceReader(fName)
				if err != nil {
					t.Fatalf("error while invoking SourceReader: %v", err)
				}

				dstContent, err := io.ReadAll(rc)
				if err != nil {
					t.Fatalf("reading merged file: %v", err)
				}

				if tC.mergedFileSize != int64(len(dstContent)) {
					t.Fatalf("wrong file size after merge, want=%d, got=%d", tC.mergedFileSize, len(dstContent))
				}

				if !bytes.Equal(srcContent, dstContent) {
					t.Fatal("merged file content doesn't match")
				}
			})
		}

		t.Run("file doesn't exist", func(t *testing.T) {
			mw := stg.(*SplitMergeMiddleware)
			mw.maxObjSize = 1024
			name := "doesnt_exist"

			_, err := mw.SourceReader(name)
			if !errors.Is(err, ErrNotExist) {
				t.Fatalf("error reported while invoking SourceReader on non-existing file: %v", err)
			}
		})

		t.Run("empty file", func(t *testing.T) {
			mw := stg.(*SplitMergeMiddleware)
			mw.maxObjSize = 1024
			fName := "empty" + randomSuffix()

			err := mw.Save(fName, strings.NewReader(""))
			if err != nil {
				t.Fatalf("Save failed: %s", err)
			}

			_, err = mw.SourceReader(fName)
			if !errors.Is(err, ErrEmpty) {
				t.Fatalf("error reported while invoking SourceReader on empty file: %v", err)
			}
		})

		t.Run("closing the stream when using split-merge middleware", func(t *testing.T) {
			mw := stg.(*SplitMergeMiddleware)
			partSize := int64(1024)
			mw.maxObjSize = partSize
			fileSize := int64(3*1014 + 512)

			fName := "test_merge_with_closing_stream" + randomSuffix()
			// create test parts
			srcContent := make([]byte, fileSize)
			r := bytes.NewReader(srcContent)
			err := mw.Save(fName, r)
			if err != nil {
				t.Fatalf("error while creating test parts: %v", err)
			}

			rc, err := mw.SourceReader(fName)
			if err != nil {
				t.Fatalf("error while invoking SourceReader: %v", err)
			}

			dstContent, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("reading merged file: %v", err)
			}
			if err = rc.Close(); err != nil {
				t.Fatalf("error while closing source reader stream: %v", err)
			}
			if fileSize != int64(len(dstContent)) {
				t.Fatalf("wrong file size after merge, want=%d, got=%d", fileSize, len(dstContent))
			}

			rc, err = mw.SourceReader(fName)
			if err != nil {
				t.Fatalf("error while invoking SourceReader for buffered reading: %v", err)
			}
			buffSize := int64(512)
			buff := make([]byte, buffSize)
			n, err := io.ReadFull(rc, buff)
			if err != nil {
				t.Fatalf("error while reading within first part: %v", err)
			}
			if n != int(buffSize) {
				t.Fatalf("wrong buff size while reading buffer: want=%d, got=%d", buffSize, n)
			}
			if err = rc.Close(); err != nil {
				t.Fatalf("error while closing source reader after partial read: %v", err)
			}

			rc, err = mw.SourceReader(fName)
			if err != nil {
				t.Fatalf("error while invoking SourceReader for buffered reading: %v", err)
			}
			buffSize = int64(1536)
			buff = make([]byte, buffSize)
			n, err = io.ReadFull(rc, buff)
			if err != nil {
				t.Fatalf("error while reading within first & second part: %v", err)
			}
			if n != int(buffSize) {
				t.Fatalf("wrong buff size while reading buffer: want=%d, got=%d", buffSize, n)
			}
			if err = rc.Close(); err != nil {
				t.Fatalf("error while closing source reader after partial read: %v", err)
			}
		})
	})

	t.Run("FileStat with split-merge middleware", func(t *testing.T) {
		testCases := []struct {
			desc          string
			partSize      int64
			totalFileSize int64
			file          string
		}{
			{
				desc:          "basic use caser for FileStat",
				partSize:      5 * 1024,
				totalFileSize: 10*5*1024 + 456,
			},
			{
				desc:          "stat for 100s of parts",
				partSize:      456,
				totalFileSize: 123*456 + 789,
			},
			{
				desc:          "stat for 1000s of parts",
				partSize:      123,
				totalFileSize: 4567*123 + 1,
			},
			{
				desc:          "single part",
				partSize:      1024 * 1024,
				totalFileSize: 1024 * 1024,
			},
			{
				desc:          "two parts",
				partSize:      1024 * 1024,
				totalFileSize: 2 * 1024 * 1024,
			},
			{
				desc:          "1 byte file",
				partSize:      20 * 1024,
				totalFileSize: 1,
			},
			{
				desc:          "file in dir",
				partSize:      4 * 1024,
				totalFileSize: 4*1024 + 456,
				file:          "sub/test_stat_in_dir",
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				mw := stg.(*SplitMergeMiddleware)
				mw.maxObjSize = tC.partSize

				fName := "test_file_stat" + randomSuffix()
				if tC.file != "" {
					fName = tC.file + randomSuffix()
				}
				// create test parts
				srcContent := make([]byte, tC.totalFileSize)
				r := bytes.NewReader(srcContent)
				err := mw.Save(fName, r)
				if err != nil {
					t.Fatalf("error while creating test parts: %v", err)
				}

				fInfo, err := mw.FileStat(fName)
				if err != nil {
					t.Fatalf("error while invoking FileStat: %v", err)
				}

				if fInfo.Name != fName {
					t.Fatalf("wrong file name, want=%s, got=%s", fName, fInfo.Name)
				}
				if fInfo.Size != tC.totalFileSize {
					t.Fatalf("wrong file size, want=%d, got=%d", tC.totalFileSize, fInfo.Size)
				}
			})
		}

		t.Run("file doesn't exist", func(t *testing.T) {
			mw := stg.(*SplitMergeMiddleware)
			fName := "test_fs" + randomSuffix()

			_, err := mw.FileStat(fName)
			if err != ErrNotExist {
				t.Fatalf("wrong error reported: want=%v, got=%v", ErrNotExist, err)
			}
		})

		t.Run("empty file", func(t *testing.T) {
			mw := stg.(*SplitMergeMiddleware)
			fName := "empty" + randomSuffix()
			err := mw.Save(fName, strings.NewReader(""))
			if err != nil {
				t.Fatalf("Save failed: %s", err)
			}

			f, err := mw.FileStat(fName)
			if err != ErrEmpty {
				t.Fatalf("wrong error reported: want=%v, got=%v", ErrEmpty, err)
			}
			if f.Name != fName && f.Size != 0 {
				t.Fatalf("wrong file info: want name=%s, size=0, got=%+v", fName, f)
			}
		})
	})

	t.Run("Delete with split-merge middleware", func(t *testing.T) {
		testCases := []struct {
			desc          string
			partSize      int64
			totalFileSize int64
			file          string
		}{
			{
				desc:          "basic use caser for Delete",
				partSize:      5 * 1024,
				totalFileSize: 10*5*1024 + 456,
			},
			{
				desc:          "stat for 100s of parts",
				partSize:      456,
				totalFileSize: 123*456 + 789,
			},
			{
				desc:          "stat for 1000s of parts",
				partSize:      123,
				totalFileSize: 4567*123 + 1,
			},
			{
				desc:          "single part",
				partSize:      1024 * 1024,
				totalFileSize: 1024 * 1024,
			},
			{
				desc:          "two parts",
				partSize:      1024 * 1024,
				totalFileSize: 2 * 1024 * 1024,
			},
			{
				desc:          "1 byte file",
				partSize:      20 * 1024,
				totalFileSize: 1,
			},
			{
				desc:          "file within dir",
				partSize:      5 * 1024,
				totalFileSize: 5*1024 + 456,
				file:          "dub/test_rm_in_dir",
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				mw := stg.(*SplitMergeMiddleware)
				mw.maxObjSize = tC.partSize

				dir := randomSuffix()
				fName := "test_rm_file"
				if tC.file != "" {
					fName = tC.file
				}
				fName = path.Join(dir, fName)

				// create test parts
				srcContent := make([]byte, tC.totalFileSize)
				r := bytes.NewReader(srcContent)
				err := mw.Save(fName, r)
				if err != nil {
					t.Fatalf("error while creating test parts: %v", err)
				}

				err = mw.Delete(fName)
				if err != nil {
					t.Fatalf("error while invoking Delete: %v", err)
				}

				fi, err := mw.List(dir, "")
				if err != nil {
					t.Fatalf("error while invoking List: %v", err)
				}

				if len(fi) != 0 {
					t.Fatalf("all files should be deleted, got %d files in dir", len(fi))
				}
			})
		}

		t.Run("file doesn't exist", func(t *testing.T) {
			mw := stg.(*SplitMergeMiddleware)
			fName := "test_rm_file" + randomSuffix()

			err := mw.Delete(fName)

			wantErr := ErrNotExist
			if err != wantErr {
				t.Fatalf("error reported while invoking Delete on non-existing file: want=%v, got=%v", wantErr, err)
			}
		})

		t.Run("empty file", func(t *testing.T) {
			mw := stg.(*SplitMergeMiddleware)
			fName := "empty" + randomSuffix()

			err := mw.Save(fName, strings.NewReader(""))
			if err != nil {
				t.Fatalf("Save failed: %s", err)
			}

			err = mw.Delete(fName)
			if err != nil {
				t.Fatalf("error reported while invoking Delete on empty file: %v", err)
			}
		})
	})

	t.Run("Copy with split-merge middleware", func(t *testing.T) {
		testCases := []struct {
			desc          string
			partSize      int64
			totalFileSize int64
			file          string
		}{
			{
				desc:          "basic use caser for Copy",
				partSize:      5 * 1024,
				totalFileSize: 10*5*1024 + 456,
			},
			{
				desc:          "stat for 100s of parts",
				partSize:      456,
				totalFileSize: 123*456 + 789,
			},
			{
				desc:          "single part",
				partSize:      1024 * 1024,
				totalFileSize: 1024 * 1024,
			},
			{
				desc:          "two parts",
				partSize:      1024 * 1024,
				totalFileSize: 2 * 1024 * 1024,
			},
			{
				desc:          "1 byte file",
				partSize:      20 * 1024,
				totalFileSize: 1,
			},
			{
				desc:          "copy from sub dir",
				partSize:      5 * 1024,
				totalFileSize: 4*5*1024 + 456,
				file:          "sub/test_copy_from_sub",
			},
		}
		for _, tC := range testCases {
			t.Run(tC.desc, func(t *testing.T) {
				mw := stg.(*SplitMergeMiddleware)
				mw.maxObjSize = tC.partSize

				fNameSrc := "test_copy" + randomSuffix()
				if tC.file != "" {
					fNameSrc = tC.file + randomSuffix()
				}
				// create test parts
				srcContent := make([]byte, tC.totalFileSize)
				r := bytes.NewReader(srcContent)
				err := mw.Save(fNameSrc, r)
				if err != nil {
					t.Fatalf("error while creating test parts: %v", err)
				}

				fNameDst := "dst/test_file_stat" + randomSuffix()

				err = mw.Copy(fNameSrc, fNameDst)
				if err != nil {
					t.Fatalf("error while copying: %v", err)
				}

				fInfoDst, err := mw.FileStat(fNameDst)
				if err != nil {
					t.Fatalf("error while invoking FileStat: %v", err)
				}

				if fInfoDst.Name != fNameDst {
					t.Fatalf("wrong file name, want=%s, got=%s", fNameDst, fInfoDst.Name)
				}
				if fInfoDst.Size != tC.totalFileSize {
					t.Fatalf("wrong file size, want=%d, got=%d", tC.totalFileSize, fInfoDst.Size)
				}
			})
		}

		t.Run("file doesn't exist", func(t *testing.T) {
			mw := stg.(*SplitMergeMiddleware)
			mw.maxObjSize = 1024

			name := "doesnt_exist" + randomSuffix()
			dstName := "dst/" + name

			err := mw.Copy(name, dstName)
			if !errors.Is(err, ErrNotExist) {
				t.Fatalf("error reported while invoking Copy on non-existing file: %v", err)
			}
		})

		t.Run("empty file", func(t *testing.T) {
			mw := stg.(*SplitMergeMiddleware)
			mw.maxObjSize = 1024

			name := "empty" + randomSuffix()
			dstName := "dst/" + name
			err := mw.Save(name, strings.NewReader(""))
			if err != nil {
				t.Fatalf("Save failed: %s", err)
			}

			err = mw.Copy(name, dstName)
			if err != nil {
				t.Fatalf("error while copying: %v", err)
			}
			_, err = mw.FileStat(dstName)
			if err != ErrEmpty {
				t.Fatalf("wrong error reported: want=%v, got=%v", ErrEmpty, err)
			}
		})
	})
}

func CalcPartSizes(partSize, totalSize int64) []int64 {
	padding := totalSize % partSize
	partsCount := totalSize / partSize

	res := make([]int64, partsCount)
	for i := range partsCount {
		res[i] = partSize
	}
	if padding > 0 {
		res = append(res, padding)
	}

	return res
}

func randomSuffix() string {
	randomBytes := make([]byte, 8)
	_, _ = rand.Read(randomBytes)
	return hex.EncodeToString(randomBytes)
}
