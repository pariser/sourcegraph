package repos

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	otlog "github.com/opentracing/opentracing-go/log"
	"github.com/pkg/errors"
	"github.com/sourcegraph/sourcegraph/pkg/api"
	"github.com/sourcegraph/sourcegraph/pkg/trace"
	"gopkg.in/inconshreveable/log15.v2"
)

// A Syncer periodically synchronizes available repositories from all its given Sources
// with the stored Repositories in Sourcegraph.
type Syncer struct {
	// FailFullSync prevents Sync from running. This should only be true for
	// Sourcegraph.com
	FailFullSync bool

	// lastSyncErr contains the last error returned by the Sourcer in each
	// Sync. It's reset with each Sync and if the sync produced no error, it's
	// set to nil.
	lastSyncErr   error
	lastSyncErrMu sync.Mutex

	store   Store
	sourcer Sourcer
	diffs   chan Diff
	now     func() time.Time

	syncSignal chan struct{}
}

// NewSyncer returns a new Syncer that syncs stored repos with
// the repos yielded by the configured sources, retrieved by the given sourcer.
// Each completed sync results in a diff that is sent to the given diffs channel.
func NewSyncer(
	store Store,
	sourcer Sourcer,
	diffs chan Diff,
	now func() time.Time,
) *Syncer {
	return &Syncer{
		store:      store,
		sourcer:    sourcer,
		diffs:      diffs,
		now:        now,
		syncSignal: make(chan struct{}, 1),
	}
}

// Run runs the Sync at the specified interval.
func (s *Syncer) Run(ctx context.Context, interval time.Duration) error {
	for ctx.Err() == nil {
		if _, err := s.Sync(ctx); err != nil {
			log15.Error("Syncer", "error", err)
		}

		select {
		case <-time.After(interval):
		case <-s.syncSignal:
		}
	}

	return ctx.Err()
}

// TriggerSync will run Sync as soon as the current Sync has finished running
// or if no Sync is running.
func (s *Syncer) TriggerSync() {
	select {
	case s.syncSignal <- struct{}{}:
	default:
	}
}

// Sync synchronizes the repositories.
func (s *Syncer) Sync(ctx context.Context) (diff Diff, err error) {
	ctx, save := s.observe(ctx, "Syncer.Sync", "")
	defer save(&diff, &err)
	defer s.setOrResetLastSyncErr(&err)

	if s.FailFullSync {
		return Diff{}, errors.New("Syncer is not enabled")
	}

	var sourced Repos
	if sourced, err = s.sourced(ctx); err != nil {
		return Diff{}, errors.Wrap(err, "syncer.sync.sourced")
	}

	store := s.store
	if tr, ok := s.store.(Transactor); ok {
		var txs TxStore
		if txs, err = tr.Transact(ctx); err != nil {
			return Diff{}, errors.Wrap(err, "syncer.sync.transact")
		}
		defer txs.Done(&err)
		store = txs
	}

	var stored Repos
	if stored, err = store.ListRepos(ctx, StoreListReposArgs{}); err != nil {
		return Diff{}, errors.Wrap(err, "syncer.sync.store.list-repos")
	}

	diff = NewDiff(sourced, stored)
	upserts := s.upserts(diff)

	if err = store.UpsertRepos(ctx, upserts...); err != nil {
		return Diff{}, errors.Wrap(err, "syncer.sync.store.upsert-repos")
	}

	if s.diffs != nil {
		s.diffs <- diff
	}

	return diff, nil
}

// StreamingSync streams repositories when syncing
func (s *Syncer) StreamingSync(ctx context.Context) (diff Diff, err error) {
	ctx, save := s.observe(ctx, "Syncer.StreamingSync", "")
	defer save(&diff, &err)
	defer s.setOrResetLastSyncErr(&err)

	if s.FailFullSync {
		return Diff{}, errors.New("Syncer is not enabled")
	}

	sourcedCtx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()

	sourced, err := s.asyncSourced(sourcedCtx)
	if err != nil {
		return Diff{}, errors.Wrap(err, "syncer.streaming-sync.async-sourced")
	}

	store := s.store
	if tr, ok := s.store.(Transactor); ok {
		var txs TxStore
		if txs, err = tr.Transact(ctx); err != nil {
			return Diff{}, errors.Wrap(err, "syncer.streaming-sync.transact")
		}
		defer txs.Done(&err)
		store = txs
	}

	errs := new(MultiSourceError)

	combinedModified := make(map[uint32]*Repo)
	combinedAdded := make(map[uint32]*Repo)
	combinedUnmodified := make(map[uint32]*Repo)
	combinedDeleted := make(map[uint32]*Repo)

	for result := range sourced {
		if result.Err != nil {
			for _, extSvc := range result.Source.ExternalServices() {
				errs.Append(&SourceError{Err: result.Err, ExtSvc: extSvc})
			}
			continue
		}

		args := StoreListReposArgs{
			Names:         []string{result.Repo.Name},
			ExternalRepos: []api.ExternalRepoSpec{result.Repo.ExternalRepo},
			UseOr:         true,
		}
		storedSubset, err := store.ListRepos(ctx, args)
		if err != nil {
			return Diff{}, errors.Wrap(err, "syncer.streaming-sync.store.list-repos")
		}

		diff = NewDiff(Repos([]*Repo{result.Repo}), storedSubset)

		upserts := s.upserts(diff)

		if err = store.UpsertRepos(ctx, upserts...); err != nil {
			return Diff{}, errors.Wrap(err, "syncer.streaming-sync.store.upsert-repos")
		}

		for _, repo := range diff.Deleted {
			combinedDeleted[repo.ID] = repo
		}

		for _, repo := range diff.Modified {
			combinedModified[repo.ID] = repo
		}

		for _, repo := range diff.Added {
			combinedAdded[repo.ID] = repo
		}

		for _, repo := range diff.Unmodified {
			combinedUnmodified[repo.ID] = repo
		}
	}

	combinedDiff := Diff{
		Modified:   make([]*Repo, 0, len(combinedModified)),
		Added:      make([]*Repo, 0, len(combinedAdded)),
		Unmodified: make([]*Repo, 0, len(combinedUnmodified)),
		Deleted:    make([]*Repo, 0, len(combinedDeleted)),
	}

	for _, repo := range combinedDeleted {
		combinedDiff.Deleted = append(combinedDiff.Deleted, repo)
	}

	for _, repo := range combinedModified {
		combinedDiff.Modified = append(combinedDiff.Modified, repo)
	}

	for _, repo := range combinedAdded {
		combinedDiff.Added = append(combinedDiff.Added, repo)
	}

	for _, repo := range combinedUnmodified {
		combinedDiff.Unmodified = append(combinedDiff.Unmodified, repo)
	}

	if s.diffs != nil {
		s.diffs <- combinedDiff
	}

	printDiff(combinedDiff)

	keepIDs := keepIDsInDiff(combinedDiff)

	if err = store.DeleteReposExcept(ctx, keepIDs...); err != nil {
		return Diff{}, errors.Wrap(err, "syncer.streaming-sync.store.delete-repos-except")
	}

	err = errs.ErrorOrNil()

	return combinedDiff, err
}

func keepIDsInDiff(d Diff) []uint32 {
	toKeep := make(map[uint32]bool)

	for _, repo := range d.Modified {
		toKeep[repo.ID] = true
	}
	for _, repo := range d.Added {
		toKeep[repo.ID] = true
	}
	for _, repo := range d.Unmodified {
		toKeep[repo.ID] = true
	}

	keepIDs := []uint32{}
	for id, keep := range toKeep {
		if keep {
			keepIDs = append(keepIDs, id)
		}
	}

	return keepIDs
}

func printDiff(diff Diff) {
	fmt.Printf("-- diff -- ")

	fmt.Printf("\tdeleted (%d)=", len(diff.Deleted))
	for _, repo := range diff.Deleted {
		fmt.Printf("%q ", repo.Name)
	}

	fmt.Printf("\tmodified=")
	for _, repo := range diff.Modified {
		fmt.Printf("%q ", repo.Name)
	}

	fmt.Printf("\tadded=")
	for _, repo := range diff.Added {
		fmt.Printf("%q ", repo.Name)
	}

	fmt.Printf("\tunmodified=%d", len(diff.Unmodified))
	fmt.Printf("\n")
}

// SyncSubset runs the syncer on a subset of the stored repositories. It will
// only sync the repositories with the same name or external service spec as
// sourcedSubset repositories.
func (s *Syncer) SyncSubset(ctx context.Context, sourcedSubset ...*Repo) (diff Diff, err error) {
	ctx, save := s.observe(ctx, "Syncer.SyncSubset", strings.Join(Repos(sourcedSubset).Names(), " "))
	defer save(&diff, &err)

	if len(sourcedSubset) == 0 {
		return Diff{}, nil
	}

	store := s.store
	if tr, ok := s.store.(Transactor); ok {
		var txs TxStore
		if txs, err = tr.Transact(ctx); err != nil {
			return Diff{}, errors.Wrap(err, "syncer.syncsubset.transact")
		}
		defer txs.Done(&err)
		store = txs
	}

	var storedSubset Repos
	args := StoreListReposArgs{
		Names:         Repos(sourcedSubset).Names(),
		ExternalRepos: Repos(sourcedSubset).ExternalRepos(),
		UseOr:         true,
	}
	if storedSubset, err = store.ListRepos(ctx, args); err != nil {
		return Diff{}, errors.Wrap(err, "syncer.syncsubset.store.list-repos")
	}

	diff = NewDiff(sourcedSubset, storedSubset)
	upserts := s.upserts(diff)

	if err = store.UpsertRepos(ctx, upserts...); err != nil {
		return Diff{}, errors.Wrap(err, "syncer.syncsubset.store.upsert-repos")
	}

	if s.diffs != nil {
		s.diffs <- diff
	}

	return diff, nil
}

func (s *Syncer) upserts(diff Diff) []*Repo {
	now := s.now()
	upserts := make([]*Repo, 0, len(diff.Added)+len(diff.Deleted)+len(diff.Modified))

	for _, repo := range diff.Deleted {
		repo.UpdatedAt, repo.DeletedAt = now, now
		repo.Sources = map[string]*SourceInfo{}
		repo.Enabled = true
		upserts = append(upserts, repo)
	}

	for _, repo := range diff.Modified {
		repo.UpdatedAt, repo.DeletedAt = now, time.Time{}
		repo.Enabled = true
		upserts = append(upserts, repo)
	}

	for _, repo := range diff.Added {
		repo.CreatedAt, repo.UpdatedAt, repo.DeletedAt = now, now, time.Time{}
		repo.Enabled = true
		upserts = append(upserts, repo)
	}

	return upserts
}

// A Diff of two sets of Diffables.
type Diff struct {
	Added      Repos
	Deleted    Repos
	Modified   Repos
	Unmodified Repos
}

// Sort sorts all Diff elements by Repo.IDs.
func (d *Diff) Sort() {
	for _, ds := range []Repos{
		d.Added,
		d.Deleted,
		d.Modified,
		d.Unmodified,
	} {
		sort.Sort(ds)
	}
}

// Repos returns all repos in the Diff.
func (d Diff) Repos() Repos {
	all := make(Repos, 0, len(d.Added)+
		len(d.Deleted)+
		len(d.Modified)+
		len(d.Unmodified))

	for _, rs := range []Repos{
		d.Added,
		d.Deleted,
		d.Modified,
		d.Unmodified,
	} {
		all = append(all, rs...)
	}

	return all
}

// NewDiff returns a diff from the given sourced and stored repos.
func NewDiff(sourced, stored []*Repo) (diff Diff) {
	// Sort sourced so we merge determinstically
	sort.Sort(Repos(sourced))

	byID := make(map[api.ExternalRepoSpec]*Repo, len(sourced))
	for _, r := range sourced {
		if !r.ExternalRepo.IsSet() {
			panic(fmt.Errorf("%s has no valid external repo spec: %s", r.Name, r.ExternalRepo))
		} else if old := byID[r.ExternalRepo]; old != nil {
			merge(old, r)
		} else {
			byID[r.ExternalRepo] = r
		}
	}

	// Ensure names are unique case-insensitively. We don't merge when finding
	// a conflict on name, we deterministically pick which sourced repo to
	// keep. Can't merge since they represent different repositories
	// (different external ID).
	byName := make(map[string]*Repo, len(byID))
	for _, r := range byID {
		k := strings.ToLower(r.Name)
		if old := byName[k]; old == nil {
			byName[k] = r
		} else {
			keep, discard := pick(r, old)
			byName[k] = keep
			delete(byID, discard.ExternalRepo)
		}
	}

	seenID := make(map[api.ExternalRepoSpec]bool, len(stored))
	seenName := make(map[string]bool, len(stored))

	// We are unsure if customer repositories can have ExternalRepo unset. We
	// know it can be unset for Sourcegraph.com. As such, we want to fallback
	// to associating stored repositories by name with the sourced
	// repositories.
	//
	// We do not want a stored repository without an externalrepo to be set
	sort.Stable(byExternalRepoSpecSet(stored))

	for _, old := range stored {
		src := byID[old.ExternalRepo]
		if src == nil && old.ExternalRepo.ID == "" && !seenName[old.Name] {
			src = byName[strings.ToLower(old.Name)]
		}

		if src == nil {
			diff.Deleted = append(diff.Deleted, old)
		} else if old.Update(src) {
			diff.Modified = append(diff.Modified, old)
		} else {
			diff.Unmodified = append(diff.Unmodified, old)
		}

		seenID[old.ExternalRepo] = true
		seenName[old.Name] = true
	}

	for _, r := range byID {
		if !seenID[r.ExternalRepo] {
			diff.Added = append(diff.Added, r)
		}
	}

	return diff
}

func merge(o, n *Repo) {
	for id, src := range o.Sources {
		n.Sources[id] = src
	}
	o.Update(n)
}

func (s *Syncer) sourced(ctx context.Context) ([]*Repo, error) {
	svcs, err := s.store.ListExternalServices(ctx, StoreListExternalServicesArgs{})
	if err != nil {
		return nil, err
	}

	srcs, err := s.sourcer(svcs...)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, sourceTimeout)
	defer cancel()

	results := make(chan *SourceResult)
	done := make(chan struct{})
	go func() {
		srcs.ListRepos(ctx, results)
		close(done)
	}()
	go func() {
		<-done
		close(results)
	}()

	var repos []*Repo
	errs := new(MultiSourceError)

	for result := range results {
		if result.Err != nil {
			for _, extSvc := range result.Source.ExternalServices() {
				errs.Append(&SourceError{Err: result.Err, ExtSvc: extSvc})
			}
			continue
		}

		repos = append(repos, result.Repo)
	}

	return repos, errs.ErrorOrNil()
}

func (s *Syncer) asyncSourced(ctx context.Context) (chan *SourceResult, error) {
	svcs, err := s.store.ListExternalServices(ctx, StoreListExternalServicesArgs{})
	if err != nil {
		return nil, err
	}

	srcs, err := s.sourcer(svcs...)
	if err != nil {
		return nil, err
	}

	results := make(chan *SourceResult)
	done := make(chan struct{})
	go func() {
		srcs.ListRepos(ctx, results)
		close(done)
	}()
	go func() {
		<-done
		close(results)
	}()

	return results, nil
}

func (s *Syncer) setOrResetLastSyncErr(perr *error) {
	var err error
	if perr != nil {
		err = *perr
	}

	s.lastSyncErrMu.Lock()
	s.lastSyncErr = err
	s.lastSyncErrMu.Unlock()
}

// LastSyncError returns the error that was produced in the last Sync run. If
// no error was produced, this returns nil.
func (s *Syncer) LastSyncError() error {
	s.lastSyncErrMu.Lock()
	defer s.lastSyncErrMu.Unlock()

	return s.lastSyncErr
}

func (s *Syncer) observe(ctx context.Context, family, title string) (context.Context, func(*Diff, *error)) {
	began := s.now()
	tr, ctx := trace.New(ctx, family, title)

	return ctx, func(d *Diff, err *error) {
		now := s.now()
		took := s.now().Sub(began).Seconds()

		fields := make([]otlog.Field, 0, 7)
		for state, repos := range map[string]Repos{
			"added":      d.Added,
			"modified":   d.Modified,
			"deleted":    d.Deleted,
			"unmodified": d.Unmodified,
		} {
			fields = append(fields, otlog.Int(state+".count", len(repos)))
			if state != "unmodified" {
				fields = append(fields,
					otlog.Object(state+".repos", repos.Names()))
			}
			syncedTotal.WithLabelValues(state).Add(float64(len(repos)))
		}

		tr.LogFields(fields...)

		lastSync.WithLabelValues().Set(float64(now.Unix()))

		success := err == nil || *err == nil
		syncDuration.WithLabelValues(strconv.FormatBool(success)).Observe(took)

		if !success {
			tr.SetError(*err)
			syncErrors.WithLabelValues().Add(1)
		}

		tr.Finish()
	}
}

type byExternalRepoSpecSet []*Repo

func (rs byExternalRepoSpecSet) Len() int      { return len(rs) }
func (rs byExternalRepoSpecSet) Swap(i, j int) { rs[i], rs[j] = rs[j], rs[i] }
func (rs byExternalRepoSpecSet) Less(i, j int) bool {
	iSet := rs[i].ExternalRepo.IsSet()
	jSet := rs[j].ExternalRepo.IsSet()
	if iSet == jSet {
		return false
	}
	return iSet
}
