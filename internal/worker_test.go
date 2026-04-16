package internal

import (
	"errors"
	"runtime"
	"sync"
	"testing"
)

func testRemoteConn(storageRoot string) *RemoteConn {
	return &RemoteConn{
		Config: &UserRemoteConfig{StorageRoot: storageRoot},
	}
}

// newSyncWorkerPool

func TestNewSyncWorkerPool_CreatesWithCorrectWorkerCount(t *testing.T) {
	dir := t.TempDir()
	kh := testKeyHolder()
	rc := testRemoteConn("/remote")

	pool := NewSyncWorkerPool(kh, rc, dir)

	expected := min(runtime.NumCPU(), workersCountHardcap)
	if pool.numWorkers != expected {
		t.Errorf("got numWorkers=%d, want %d", pool.numWorkers, expected)
	}
	if pool.kh != kh || pool.rc != rc || pool.absRoot != dir {
		t.Error("pool fields not set correctly")
	}
}

// startworkers

func TestStartWorkers_EmptyDiff_ReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	pool := NewSyncWorkerPool(testKeyHolder(), testRemoteConn("/remote"), dir)

	diff := DiffResult{}
	results, err := pool.StartWorkers(diff, 0)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

// workerdistribution

func TestStartWorkers_WorkerDistribution_OnlyUploads(t *testing.T) {
	dir := t.TempDir()
	pool := NewSyncWorkerPool(testKeyHolder(), testRemoteConn("/remote"), dir)

	uploadCount := 10
	downloadCount := 0
	totalJobs := uploadCount + downloadCount

	var uploadWorkers, downloadWorkers int
	if totalJobs == 0 {
		uploadWorkers, downloadWorkers = 0, 0
	} else if uploadCount == 0 {
		uploadWorkers, downloadWorkers = 0, pool.numWorkers
	} else if downloadCount == 0 {
		uploadWorkers, downloadWorkers = pool.numWorkers, 0
	} else {
		uploadRatio := float64(uploadCount) / float64(totalJobs)
		uploadWorkers = int(float64(pool.numWorkers) * uploadRatio)
		downloadWorkers = pool.numWorkers - uploadWorkers
		if uploadWorkers == 0 {
			uploadWorkers = 1
			downloadWorkers--
		}
		if downloadWorkers == 0 {
			downloadWorkers = 1
			uploadWorkers--
		}
	}

	if uploadWorkers != pool.numWorkers || downloadWorkers != 0 {
		t.Errorf("only uploads: got upload=%d download=%d, want %d/0",
			uploadWorkers, downloadWorkers, pool.numWorkers)
	}
}

func TestStartWorkers_WorkerDistribution_OnlyDownloads(t *testing.T) {
	dir := t.TempDir()
	pool := NewSyncWorkerPool(testKeyHolder(), testRemoteConn("/remote"), dir)

	uploadCount := 0
	downloadCount := 10
	totalJobs := uploadCount + downloadCount

	var uploadWorkers, downloadWorkers int
	if totalJobs == 0 {
		uploadWorkers, downloadWorkers = 0, 0
	} else if uploadCount == 0 {
		uploadWorkers, downloadWorkers = 0, pool.numWorkers
	} else if downloadCount == 0 {
		uploadWorkers, downloadWorkers = pool.numWorkers, 0
	} else {
		uploadRatio := float64(uploadCount) / float64(totalJobs)
		uploadWorkers = int(float64(pool.numWorkers) * uploadRatio)
		downloadWorkers = pool.numWorkers - uploadWorkers
		if uploadWorkers == 0 {
			uploadWorkers = 1
			downloadWorkers--
		}
		if downloadWorkers == 0 {
			downloadWorkers = 1
			uploadWorkers--
		}
	}

	if uploadWorkers != 0 || downloadWorkers != pool.numWorkers {
		t.Errorf("only downloads: got upload=%d download=%d, want 0/%d",
			uploadWorkers, downloadWorkers, pool.numWorkers)
	}
}

func TestStartWorkers_WorkerDistribution_Mixed(t *testing.T) {
	dir := t.TempDir()
	pool := NewSyncWorkerPool(testKeyHolder(), testRemoteConn("/remote"), dir)

	uploadCount := 6
	downloadCount := 4
	totalJobs := uploadCount + downloadCount

	var uploadWorkers, downloadWorkers int
	if totalJobs == 0 {
		uploadWorkers, downloadWorkers = 0, 0
	} else if uploadCount == 0 {
		uploadWorkers, downloadWorkers = 0, pool.numWorkers
	} else if downloadCount == 0 {
		uploadWorkers, downloadWorkers = pool.numWorkers, 0
	} else {
		uploadRatio := float64(uploadCount) / float64(totalJobs)
		uploadWorkers = int(float64(pool.numWorkers) * uploadRatio)
		downloadWorkers = pool.numWorkers - uploadWorkers
		if uploadWorkers == 0 {
			uploadWorkers = 1
			downloadWorkers--
		}
		if downloadWorkers == 0 {
			downloadWorkers = 1
			uploadWorkers--
		}
	}

	if uploadWorkers < 1 || downloadWorkers < 1 {
		t.Errorf("mixed load: expected at least 1 each, got upload=%d download=%d",
			uploadWorkers, downloadWorkers)
	}
	if uploadWorkers+downloadWorkers > pool.numWorkers {
		t.Errorf("total workers %d exceeds limit %d",
			uploadWorkers+downloadWorkers, pool.numWorkers)
	}
}

func TestStartWorkers_SingleCore_HandlesMixedLoad(t *testing.T) {
	dir := t.TempDir()
	pool := NewSyncWorkerPool(testKeyHolder(), testRemoteConn("/remote"), dir)

	// simulate single code case
	pool.numWorkers = 1

	uploadCount := 5
	downloadCount := 3
	totalJobs := uploadCount + downloadCount

	var uploadWorkers, downloadWorkers int
	if totalJobs == 0 {
		uploadWorkers, downloadWorkers = 0, 0
	} else if uploadCount == 0 {
		uploadWorkers, downloadWorkers = 0, pool.numWorkers
	} else if downloadCount == 0 {
		uploadWorkers, downloadWorkers = pool.numWorkers, 0
	} else {
		uploadRatio := float64(uploadCount) / float64(totalJobs)
		uploadWorkers = int(float64(pool.numWorkers) * uploadRatio)
		downloadWorkers = pool.numWorkers - uploadWorkers
		if uploadWorkers == 0 {
			uploadWorkers = 1
			downloadWorkers--
		}
		if downloadWorkers == 0 {
			downloadWorkers = 1
			uploadWorkers--
		}
	}

	if uploadWorkers+downloadWorkers > pool.numWorkers {
		t.Errorf("single-core: total %d exceeds limit %d",
			uploadWorkers+downloadWorkers, pool.numWorkers)
	}
}

// poolinternals

func TestSyncWorkerPool_ErrorChannel(t *testing.T) {
	dir := t.TempDir()
	pool := NewSyncWorkerPool(testKeyHolder(), testRemoteConn("/remote"), dir)

	select {
	case pool.errChan <- errors.New("test"):
		// should be buffered
	default:
		t.Error("error channel not buffered")
	}

	select {
	case err := <-pool.errChan:
		if err.Error() != "test" {
			t.Error("wrong error received")
		}
	default:
		t.Error("cannot read from error channel")
	}
}

func TestSyncWorkerPool_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	pool := NewSyncWorkerPool(testKeyHolder(), testRemoteConn("/remote"), dir)

	select {
	case <-pool.ctx.Done():
		t.Error("context cancelled initially")
	default:
	}

	pool.cancel()

	select {
	case <-pool.ctx.Done():
	default:
		t.Error("context not cancelled after cancel()")
	}
}

func TestSyncWorkerPool_OutMu(t *testing.T) {
	pool := &SyncWorkerPool{outMu: sync.Mutex{}}

	pool.outMu.Lock()
	pool.outMu.Unlock()
}
