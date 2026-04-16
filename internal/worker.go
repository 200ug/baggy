package internal

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sync"
)

const workersCountHardcap = 12

type SyncWorkerPool struct {
	numWorkers int
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	errChan    chan error // buffer should be 1 for fail-fast logic

	kh      *KeyHolder
	rc      *RemoteConn
	absRoot string

	uploadJobs   chan Filedata
	downloadJobs chan Filedata
	metaResults  chan Filedata

	outMu sync.Mutex
}

func NewSyncWorkerPool(kh *KeyHolder, rc *RemoteConn, absRoot string) *SyncWorkerPool {
	ctx, cancel := context.WithCancel(context.Background())
	return &SyncWorkerPool{
		numWorkers:  min(runtime.NumCPU(), workersCountHardcap),
		ctx:         ctx,
		cancel:      cancel,
		errChan:     make(chan error, 1),
		kh:          kh,
		rc:          rc,
		absRoot:     absRoot,
		metaResults: make(chan Filedata),
	}
}

func (wp *SyncWorkerPool) StartWorkers(diff DiffResult, totalJobs int) ([]Filedata, error) {
	wp.uploadJobs = make(chan Filedata, len(diff.ToUpload))
	wp.downloadJobs = make(chan Filedata, len(diff.ToDownload))

	// scale worker count based on load distribution among uploads/downloads
	var uploadWorkers, downloadWorkers int
	if totalJobs == 0 {
		uploadWorkers, downloadWorkers = 0, 0
	} else if len(diff.ToUpload) == 0 {
		uploadWorkers, downloadWorkers = 0, wp.numWorkers
	} else if len(diff.ToDownload) == 0 {
		uploadWorkers, downloadWorkers = wp.numWorkers, 0
	} else {
		// both -> proportional split with minimum 1 each
		uploadRatio := float64(len(diff.ToUpload)) / float64(totalJobs)
		uploadWorkers = int(float64(wp.numWorkers) * uploadRatio)
		downloadWorkers = wp.numWorkers - uploadWorkers
		if uploadWorkers == 0 {
			uploadWorkers = 1
			downloadWorkers--
		}
		if downloadWorkers == 0 {
			downloadWorkers = 1
			uploadWorkers--
		}
	}

	// handle results (updated metadata entries) collection
	metaResults := make([]Filedata, 0, len(diff.ToDownload))
	go func() {
		for fd := range wp.metaResults {
			metaResults = append(metaResults, fd)
		}
	}()

	// start workers
	for i := 0; i < uploadWorkers; i++ {
		wp.wg.Add(1)
		go wp.uploadWorker()
	}
	for i := 0; i < downloadWorkers; i++ {
		wp.wg.Add(1)
		go wp.downloadWorker()
	}

	// feed jobs
	go func() {
		for _, f := range diff.ToUpload {
			select {
			case wp.uploadJobs <- f:
			case <-wp.ctx.Done():
				return
			}
		}
		close(wp.uploadJobs)
	}()
	go func() {
		for _, f := range diff.ToDownload {
			select {
			case wp.downloadJobs <- f:
			case <-wp.ctx.Done():
				return
			}
		}
		close(wp.downloadJobs)
	}()

	done := make(chan struct{})
	go func() {
		wp.wg.Wait()
		close(wp.metaResults)
		close(done)
	}()

	select {
	case err := <-wp.errChan:
		wp.cancel()
		<-done
		return []Filedata{}, err // don't return partial metaResults
	case <-done:
		return metaResults, nil
	}
}

func (wp *SyncWorkerPool) uploadWorker() {
	defer wp.wg.Done()

	for {
		select {
		case <-wp.ctx.Done():
			return
		case job, ok := <-wp.uploadJobs:
			if !ok {
				return
			}
			if err := wp.processUpload(job); err != nil {
				select {
				case wp.errChan <- err:
				default:
				}
				return
			}
		}
	}
}

func (wp *SyncWorkerPool) processUpload(job Filedata) error {
	rel := job.LocalPath
	remotePath := path.Join(wp.rc.Config.StorageRoot, filepath.Base(wp.absRoot), rel+"."+FileExt)

	tmp, err := os.CreateTemp("", "wsftp-enc-*")
	if err != nil {
		return fmt.Errorf("upload %s: create temp: %w", rel, err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err = wp.kh.EncryptFile(filepath.Join(wp.absRoot, rel), tmpPath); err != nil {
		return fmt.Errorf("upload %s: encrypt: %w", rel, err)
	}

	if err = wp.rc.PushFile(tmpPath, remotePath); err != nil {
		return fmt.Errorf("upload %s: push: %w", rel, err)
	}

	wp.outMu.Lock()
	fmt.Printf("[>] %s\n", rel)
	wp.outMu.Unlock()

	return nil
}

func (wp *SyncWorkerPool) downloadWorker() {
	defer wp.wg.Done()

	for {
		select {
		case <-wp.ctx.Done():
			return
		case job, ok := <-wp.downloadJobs:
			if !ok {
				return
			}
			if err := wp.processDownload(job); err != nil {
				select {
				case wp.errChan <- err:
				default:
				}
				return
			}
		}
	}
}

func (wp *SyncWorkerPool) processDownload(job Filedata) error {
	rel := job.LocalPath
	remotePath := path.Join(wp.rc.Config.StorageRoot, filepath.Base(wp.absRoot), rel+"."+FileExt)
	dst := filepath.Join(wp.absRoot, rel)

	tmp, err := os.CreateTemp("", "wsftp-enc-*")
	if err != nil {
		return fmt.Errorf("download %s: create temp: %w", rel, err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	if err = wp.rc.PullFile(remotePath, tmpPath); err != nil {
		return fmt.Errorf("download %s: pull: %w", rel, err)
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("download %s: mkdir: %w", rel, err)
	}
	if err = wp.kh.DecryptFile(tmpPath, dst); err != nil {
		return fmt.Errorf("download %s: decrypt: %w", rel, err)
	}

	// meta entries are propagated to main logic loop in sync, no need to worry about race conditions
	info, err := os.Stat(dst)
	if err != nil {
		return fmt.Errorf("download %s: stat: %w", rel, err)
	}
	hash, err := HashFile(dst)
	if err != nil {
		return fmt.Errorf("download %s: hash: %w", rel, err)
	}
	entry := Filedata{LocalPath: rel, ContentHash: hash, ModifiedAt: info.ModTime().Unix()}
	select {
	case wp.metaResults <- entry:
	case <-wp.ctx.Done():
		// notably might lose results if the timing is unfortunate, not worth spending time to fix this for now
		return wp.ctx.Err()
	}

	wp.outMu.Lock()
	fmt.Printf("[<] %s\n", rel)
	wp.outMu.Unlock()

	return nil
}
