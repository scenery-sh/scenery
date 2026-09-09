package storagefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"scenery.sh/internal/atomicfile"
)

// These concrete boundaries permit deterministic failure-cut tests. Production
// always uses checked synchronization; there is no runtime durability switch.
type diskIO struct {
	syncFile     func(*os.File) error
	replace      func(*os.Root, string, []byte) error
	rename       func(*os.Root, string, string) error
	remove       func(*os.Root, string) error
	openPayload  func(*os.Root, string) (*os.File, error)
	clonePayload func(*os.File, *os.Root, string) (*os.File, bool, error)
}

func durableIO() diskIO {
	return diskIO{
		syncFile:     func(f *os.File) error { return f.Sync() },
		rename:       func(r *os.Root, from, to string) error { return r.Rename(from, to) },
		remove:       func(r *os.Root, name string) error { return r.Remove(name) },
		openPayload:  func(r *os.Root, name string) (*os.File, error) { return openOwned(r, name, os.O_RDONLY) },
		clonePayload: clonePayload,
		replace: func(r *os.Root, name string, data []byte) error {
			return atomicfile.WriteRoot(r, name, data, 0o600, atomicfile.Options{SyncFile: true, SyncDir: true})
		},
	}
}

func (d diskIO) syncDirectory(r *os.Root, name string) error {
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	err = d.syncFile(f)
	closeErr := f.Close()
	return errors.Join(err, closeErr)
}

func (d diskIO) writeRecord(r *os.Root, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > maxRecordBytes {
		return fmt.Errorf("%w: oversized record", ErrInvalid)
	}
	return d.replace(r, name, data)
}
