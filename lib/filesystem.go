package bucketsync

import (
	"bytes"
	"sync"
	"time"

	"encoding/json"

	"github.com/hanwen/go-fuse/fuse"
	"go.uber.org/zap"
)

// Meta is common struct for directory, file and symlink
type Meta struct {
	Size  int64     `json:"size"`
	Mode  uint32    `json:"mode"`
	UID   uint32    `json:"uid"`
	GID   uint32    `json:"gid"`
	Atime time.Time `json:"atime"`
	Ctime time.Time `json:"ctime"`
	Mtime time.Time `json:"mtime"`
}

// Node is common part of Directory, File, and SymLink
type Node struct {
	Key  ObjectKey `json:"key"`
	Meta Meta      `json:"meta"`
}

type Directory struct {
	Key      ObjectKey            `json:"key"`
	Meta     Meta                 `json:"meta"`
	FileMeta map[string]ObjectKey `json:"children"`
	sess     *Session
}

func (o *Directory) Save() error {
	result, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return o.sess.s3.UploadWithCache(o.Key, bytes.NewReader(result))
}

type File struct {
	Key        ObjectKey         `json:"key"`
	Meta       Meta              `json:"meta"`
	ExtentSize int64             `json:"extent_size"`
	Extent     map[int64]*Extent `json:"extent"`
	sess       *Session
	dirty      bool
}

func (o *File) Save() error {
	wg := sync.WaitGroup{}
	errc := make(chan error)
	done := make(chan struct{})
	for _, e := range o.Extent {
		wg.Add(1)
		go func(e *Extent) {
			if !e.dirty {
				wg.Done()
				return
			}
			key := e.CurrentKey()
			if o.sess.s3.IsExist(key) {
				wg.Done()
				return
			}
			err := o.sess.s3.Upload(key, bytes.NewReader(e.body))
			if err != nil {
				errc <- err
				return
			}
			e.dirty = false
			wg.Done()
		}(e)
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case err := <-errc:
		return err
	case <-done:
		result, err := json.Marshal(o)
		if err != nil {
			return err
		}
		err = o.sess.s3.UploadWithCache(o.Key, bytes.NewReader(result))
		if err != nil {
			return err
		}
		return nil
	}

}

type Extent struct {
	Key   ObjectKey `json:"key"`
	body  []byte    // call Fill() to use this
	dirty bool
	sess  *Session
}

func (e *Extent) CurrentKey() ObjectKey {
	return e.sess.KeyGen(e.body)
}

func (e *Extent) Fill() error {
	if e.dirty || len(e.body) != 0 {
		e.sess.logger.Debug("Already filled")
		return nil
	}
	body, err := e.sess.s3.Download(e.Key)
	if err != nil {
		return err
	}
	e.body = body
	e.sess.logger.Debug("Fill Extent", zap.Int("body size", len(e.body)))
	return nil
}

type SymLink struct {
	Key    ObjectKey `json:"key"`
	Meta   Meta      `json:"meta"`
	LinkTo string    `json:"linkto"`
	sess   *Session
}

func (o *SymLink) Save() error {
	result, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return o.sess.s3.UploadWithCache(o.Key, bytes.NewReader(result))
}

func NewMeta(mode uint32, context *fuse.Context) Meta {
	meta := Meta{
		Mode:  mode,
		Size:  0,
		UID:   context.Uid,
		GID:   context.Gid,
		Atime: time.Now(),
		Ctime: time.Now(),
		Mtime: time.Now(),
	}
	return meta
}
