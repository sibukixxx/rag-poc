package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sibukixxx/rag-poc/internal/domain/knowledge"
	"github.com/sibukixxx/rag-poc/internal/domain/source"
	"github.com/sibukixxx/rag-poc/internal/usecase"
)

type fakeSourceStore struct {
	connection source.Connection
	items      map[string]source.Item
	jobs       map[string]source.SyncJob
}

func newFakeSourceStore() *fakeSourceStore {
	return &fakeSourceStore{items: map[string]source.Item{}, jobs: map[string]source.SyncJob{}}
}

func (s *fakeSourceStore) CreateConnection(_ context.Context, c source.Connection) error {
	s.connection = c
	return nil
}
func (s *fakeSourceStore) GetConnection(_ context.Context, id string) (*source.Connection, error) {
	if s.connection.ID != id {
		return nil, source.ErrNotFound
	}
	c := s.connection
	return &c, nil
}
func (s *fakeSourceStore) ListConnections(context.Context, string) ([]source.Connection, error) {
	return []source.Connection{s.connection}, nil
}
func (s *fakeSourceStore) UpdateCursor(_ context.Context, id, cursor string, _ time.Time) error {
	if s.connection.ID != id {
		return source.ErrNotFound
	}
	s.connection.Cursor = cursor
	return nil
}
func sourceItemKey(connectionID, externalID string) string { return connectionID + "\x00" + externalID }
func (s *fakeSourceStore) GetItem(_ context.Context, connectionID, externalID string) (*source.Item, error) {
	item, ok := s.items[sourceItemKey(connectionID, externalID)]
	if !ok {
		return nil, source.ErrNotFound
	}
	copy := item
	return &copy, nil
}
func (s *fakeSourceStore) ReplaceItemDocument(_ context.Context, item source.Item) error {
	s.items[sourceItemKey(item.ConnectionID, item.ExternalID)] = item
	return nil
}
func (s *fakeSourceStore) DeleteItemDocument(_ context.Context, connectionID, externalID string, at time.Time) (bool, error) {
	key := sourceItemKey(connectionID, externalID)
	item, ok := s.items[key]
	if !ok || item.DocumentID == "" {
		return false, nil
	}
	item.DocumentID = ""
	item.DeletedAt = &at
	s.items[key] = item
	return true, nil
}
func (s *fakeSourceStore) DeleteDocument(context.Context, string) error { return nil }
func (s *fakeSourceStore) CreateJob(_ context.Context, job source.SyncJob) error {
	s.jobs[job.ID] = job
	return nil
}
func (s *fakeSourceStore) FinishJob(_ context.Context, job source.SyncJob) error {
	s.jobs[job.ID] = job
	return nil
}
func (s *fakeSourceStore) GetJob(_ context.Context, id string) (*source.SyncJob, error) {
	job, ok := s.jobs[id]
	if !ok {
		return nil, source.ErrNotFound
	}
	return &job, nil
}

type fakeConnector struct {
	batches []source.Batch
	cursors []string
	errAt   int
}

func (c *fakeConnector) Provider() string { return "slack" }
func (c *fakeConnector) Pull(_ context.Context, _ source.Connection, cursor string) (source.Batch, error) {
	c.cursors = append(c.cursors, cursor)
	index := len(c.cursors) - 1
	if c.errAt > 0 && index+1 == c.errAt {
		return source.Batch{}, errors.New("provider unavailable")
	}
	if index >= len(c.batches) {
		return source.Batch{}, fmt.Errorf("unexpected pull %d", index)
	}
	return c.batches[index], nil
}

type fakeRegistry struct{ connector source.Connector }

func (r fakeRegistry) Get(provider string) (source.Connector, bool) {
	return r.connector, provider == r.connector.Provider()
}

type fakeTextIngester struct {
	calls  int
	failOn int
}

func (i *fakeTextIngester) IngestText(_ context.Context, kbID, title, mimeType, text string) (*knowledge.Document, error) {
	i.calls++
	if i.failOn > 0 && i.calls == i.failOn {
		return nil, errors.New("embedding failed")
	}
	return &knowledge.Document{ID: fmt.Sprintf("doc-%d", i.calls), KnowledgeBaseID: kbID, Filename: title}, nil
}

func TestSourceSyncCreatesUpdatesSkipsDeletesAndAdvancesCursor(t *testing.T) {
	store := newFakeSourceStore()
	store.connection = source.Connection{ID: "conn", KnowledgeBaseID: "kb", Provider: "slack", Enabled: true}
	updated := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	connector := &fakeConnector{batches: []source.Batch{
		{
			Documents: []source.Document{
				{ExternalID: "A", Title: "Thread A", Body: "first", SourceURL: "https://slack.test/A", Visibility: []string{"C1"}},
				{ExternalID: "B", Title: "Thread B", Body: "second"},
			},
			NextCursor: "cursor-1", HasMore: true,
		},
		{
			Documents: []source.Document{
				{ExternalID: "A", Title: "Thread A", Body: "first", SourceURL: "https://slack.test/A", Visibility: []string{"C1"}},
				{ExternalID: "B", Deleted: true},
				{ExternalID: "C", Title: "Thread C", Body: "third", UpdatedAt: &updated},
			},
			NextCursor: "cursor-2",
		},
	}}
	ingester := &fakeTextIngester{}
	clock := updated
	ids := 0
	uc := usecase.NewSourceSyncUseCase(store, fakeRegistry{connector}, ingester)
	uc.Now = func() time.Time { clock = clock.Add(time.Second); return clock }
	uc.NewID = func() string { ids++; return fmt.Sprintf("id-%d", ids) }

	job, err := uc.Sync(context.Background(), store.connection.ID)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if job.Status != source.JobStatusCompleted || job.Created != 3 || job.Updated != 0 || job.Deleted != 1 || job.Skipped != 1 {
		t.Fatalf("unexpected job: %+v", job)
	}
	if ingester.calls != 3 {
		t.Fatalf("expected three ingests, got %d", ingester.calls)
	}
	if store.connection.Cursor != "cursor-2" {
		t.Fatalf("cursor not advanced: %q", store.connection.Cursor)
	}
	if len(connector.cursors) != 2 || connector.cursors[0] != "" || connector.cursors[1] != "cursor-1" {
		t.Fatalf("connector cursors: %#v", connector.cursors)
	}
	itemB, _ := store.GetItem(context.Background(), "conn", "B")
	if itemB.DeletedAt == nil || itemB.DocumentID != "" {
		t.Fatalf("item B not deleted: %+v", itemB)
	}
}

func TestSourceSyncReingestsChangedItem(t *testing.T) {
	store := newFakeSourceStore()
	store.connection = source.Connection{ID: "conn", KnowledgeBaseID: "kb", Provider: "slack", Enabled: true}
	connector := &fakeConnector{batches: []source.Batch{{Documents: []source.Document{{ExternalID: "A", Body: "version 1"}}, NextCursor: "1"}}}
	ingester := &fakeTextIngester{}
	uc := usecase.NewSourceSyncUseCase(store, fakeRegistry{connector}, ingester)
	uc.NewID = func() string { return fmt.Sprintf("id-%d", ingester.calls) }
	if _, err := uc.Sync(context.Background(), "conn"); err != nil {
		t.Fatalf("first Sync: %v", err)
	}
	connector.batches = []source.Batch{{Documents: []source.Document{{ExternalID: "A", Body: "version 2"}}, NextCursor: "2"}}
	connector.cursors = nil
	job, err := uc.Sync(context.Background(), "conn")
	if err != nil {
		t.Fatalf("second Sync: %v", err)
	}
	if job.Updated != 1 || job.Created != 0 || ingester.calls != 2 {
		t.Fatalf("unexpected update: job=%+v calls=%d", job, ingester.calls)
	}
}

func TestSourceSyncDoesNotAdvancePartialBatchCursorOnFailure(t *testing.T) {
	store := newFakeSourceStore()
	store.connection = source.Connection{ID: "conn", KnowledgeBaseID: "kb", Provider: "slack", Cursor: "before", Enabled: true}
	connector := &fakeConnector{batches: []source.Batch{{
		Documents:  []source.Document{{ExternalID: "A", Body: "ok"}, {ExternalID: "B", Body: "fails"}},
		NextCursor: "after",
	}}}
	ingester := &fakeTextIngester{failOn: 2}
	uc := usecase.NewSourceSyncUseCase(store, fakeRegistry{connector}, ingester)

	job, err := uc.Sync(context.Background(), "conn")
	if err == nil || job.Status != source.JobStatusFailed {
		t.Fatalf("expected failed job, got job=%+v err=%v", job, err)
	}
	if store.connection.Cursor != "before" {
		t.Fatalf("partial batch advanced cursor to %q", store.connection.Cursor)
	}
	storedJob, _ := store.GetJob(context.Background(), job.ID)
	if storedJob.Status != source.JobStatusFailed || storedJob.FinishedAt == nil {
		t.Fatalf("failed job was not persisted: %+v", storedJob)
	}
}
