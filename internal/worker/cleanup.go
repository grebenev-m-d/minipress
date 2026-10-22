package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"minipress/internal/repository"
	"minipress/internal/service"
)

func NewDraftCleanupJob(pages *repository.PageRepo, minAge time.Duration, clock Clock) Job {
	return Job{
		Name: "cleanup-empty-drafts",
		Run: func(ctx context.Context) (string, error) {
			n, err := pages.DeleteEmptyDrafts(ctx, clock.Now().Add(-minAge))
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("deleted drafts: %d", n), nil
		},
	}
}

const imageBatch = 200

func NewImageCleanupJob(images *repository.ImageRepo, store *service.ImageStore, grace time.Duration, clock Clock, log *slog.Logger) Job {
	return Job{
		Name: "cleanup-unused-images",
		Run: func(ctx context.Context) (string, error) {
			adopted, err := Reconcile(ctx, images, store)
			if err != nil {
				return "", fmt.Errorf("reconcile disk: %w", err)
			}

			cutoff := clock.Now().Add(-grace)
			var removed, failed int

			for {
				batch, err := images.ListOrphanedBefore(ctx, cutoff, imageBatch)
				if err != nil {
					return "", err
				}
				if len(batch) == 0 {
					break
				}
				for _, im := range batch {
					if err := ctx.Err(); err != nil {
						return "", err
					}

					deleted, err := images.DeleteIfOrphaned(ctx, im.ID)
					if err != nil {
						return "", err
					}
					if !deleted {
						continue
					}
					if err := store.Remove(im.FileName); err != nil {

						log.Warn("could not remove file", "file", im.FileName, "err", err)
						failed++
						continue
					}
					removed++
				}
			}
			return fmt.Sprintf("removed files: %d, failures: %d, untracked found: %d", removed, failed, adopted), nil
		},
	}
}

func Reconcile(ctx context.Context, images *repository.ImageRepo, store *service.ImageStore) (int, error) {
	files, err := store.List()
	if err != nil {
		return 0, err
	}
	known, err := images.KnownFileNames(ctx)
	if err != nil {
		return 0, err
	}
	adopted := 0
	for _, name := range files {
		if _, ok := known[name]; ok {
			continue
		}
		if err := images.Adopt(ctx, name); err != nil {
			return adopted, err
		}
		adopted++
	}
	return adopted, nil
}
