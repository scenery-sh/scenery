package storagefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"

	"scenery.sh/internal/machine"
)

// Generation is a private restore target, usable only during Stage. It does not
// expose live object mutation or acquire a second maintenance lease.
type Generation struct {
	namespace   *Namespace
	lease       *namespaceLease
	id          string
	Conflicts   int64
	Skipped     int64
	Overwritten int64
	Cloned      int64
	Copied      int64
}

func (g *Generation) Put(ctx context.Context, object Object, body io.Reader, conflict string) error {
	if err := ValidateLogicalObject(object); err != nil {
		return err
	}
	if body == nil || (conflict != "fail" && conflict != "skip" && conflict != "overwrite") {
		return ErrInvalid
	}
	scope := Scope{Store: object.Store, Tenant: object.Tenant}
	_, readErr := readReference(g.lease.root, g.id, scope, object.Key)
	if readErr != nil && !errors.Is(readErr, ErrNotFound) {
		return readErr
	}
	if readErr == nil {
		g.Conflicts++
		if conflict == "fail" {
			return fmt.Errorf("%w: snapshot tuple already exists", ErrPrecondition)
		}
		if conflict == "skip" {
			g.Skipped++
			return nil
		}
		g.Overwritten++
	}
	version, err := randomID()
	if err != nil {
		return err
	}
	etag, err := randomID()
	if err != nil {
		return err
	}
	payload := scope.versionPath(g.id, object.Key, version)
	if err := g.namespace.io.makeParents(g.lease.root, payload); err != nil {
		return err
	}
	f, cloned, err := g.openPayload(body, payload)
	if err != nil {
		return err
	}
	input, output := body, io.Writer(f)
	if cloned {
		input, output = f, io.Discard
	}
	hash := sha256.New()
	size, copyErr := io.CopyBuffer(io.MultiWriter(output, hash), io.LimitReader(contextReader{ctx: ctx, reader: input}, object.SizeBytes), make([]byte, 32<<10))
	if copyErr == nil && size != object.SizeBytes {
		copyErr = io.ErrUnexpectedEOF
	}
	if copyErr == nil {
		var extra [1]byte
		n, err := input.Read(extra[:])
		if n != 0 || !errors.Is(err, io.EOF) {
			copyErr = fmt.Errorf("%w: snapshot payload length differs", ErrCorrupt)
		}
	}
	if copyErr == nil && hex.EncodeToString(hash.Sum(nil)) != object.SHA256 {
		copyErr = fmt.Errorf("%w: snapshot payload digest differs", ErrCorrupt)
	}
	if copyErr == nil {
		copyErr = ctx.Err()
	}
	if copyErr == nil {
		copyErr = g.namespace.io.syncFile(f)
	}
	if err := errors.Join(copyErr, f.Close()); err != nil {
		return err
	}
	if err := g.namespace.io.syncDirectory(g.lease.root, filepath.Dir(payload)); err != nil {
		return err
	}
	object.ETag = `"` + etag + `"`
	object.Metadata = maps.Clone(object.Metadata)
	ref := reference{ArtifactIdentity: machine.NewArtifactIdentity(referenceKind, referenceDescriptor), Object: object, VersionID: version}
	name := scope.refPath(g.id, object.Key)
	if err := g.namespace.io.makeParents(g.lease.root, name); err != nil {
		return err
	}
	if err := g.namespace.io.writeRecord(g.lease.root, name, ref); err != nil {
		return err
	}
	if cloned {
		g.Cloned++
	} else {
		g.Copied++
	}
	return nil
}

func (g *Generation) openPayload(body io.Reader, path string) (*os.File, bool, error) {
	if source, ok := body.(*os.File); ok && g.namespace.io.clonePayload != nil {
		ready, err := cloneSourceReady(source)
		if err != nil {
			return nil, false, err
		}
		if ready {
			f, cloned, err := g.namespace.io.clonePayload(source, g.lease.root, path)
			if err != nil || cloned {
				return f, cloned, err
			}
		}
	}
	f, err := g.lease.root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	return f, false, err
}
