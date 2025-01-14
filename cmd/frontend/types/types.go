// Package types defines types used by the frontend.
package types

import (
	"time"

	"github.com/sourcegraph/sourcegraph/pkg/api"
)

// RepoFields are lazy loaded data fields on a Repo (from the DB).
type RepoFields struct {
	// URI is the full name for this repository (e.g.,
	// "github.com/user/repo"). See the documentation for the Name field.
	URI string

	// Description is a brief description of the repository.
	Description string

	// DEPRECATED: this field is always empty for new repositories as of
	// https://github.com/sourcegraph/sourcegraph/issues/2586. Do not use it.
	//
	// Language is the primary programming language used in this repository.
	Language string

	// Fork is whether this repository is a fork of another repository.
	Fork bool
}

// Repo represents a source code repository.
type Repo struct {
	// ID is the unique numeric ID for this repository.
	ID api.RepoID
	// ExternalRepo identifies this repository by its ID on the external service where it resides (and the external
	// service itself).
	ExternalRepo api.ExternalRepoSpec
	// Name is the name for this repository (e.g., "github.com/user/repo"). It
	// is the same as URI, unless the user configures a non-default
	// repositoryPathPattern.
	//
	// Previously, this was called RepoURI.
	Name api.RepoName

	// RepoFields contains fields that are loaded from the DB only when necessary.
	// This is to reduce memory usage when loading thousands of repos.
	*RepoFields
}

// Repos is an utility type of a list of repos.
type Repos []*Repo

func (rs Repos) Len() int           { return len(rs) }
func (rs Repos) Less(i, j int) bool { return rs[i].ID < rs[j].ID }
func (rs Repos) Swap(i, j int)      { rs[i], rs[j] = rs[j], rs[i] }

// ExternalService is a connection to an external service.
type ExternalService struct {
	ID          int64
	Kind        string
	DisplayName string
	Config      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

type GlobalState struct {
	SiteID      string
	Initialized bool // whether the initial site admin account has been created
}

// User represents a registered user.
type User struct {
	ID          int32
	Username    string
	DisplayName string
	AvatarURL   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	SiteAdmin   bool
	Tags        []string
}

type Org struct {
	ID          int32
	Name        string
	DisplayName *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type OrgMembership struct {
	ID        int32
	OrgID     int32
	UserID    int32
	CreatedAt time.Time
	UpdatedAt time.Time
}

type PhabricatorRepo struct {
	ID       int32
	Name     api.RepoName
	URL      string
	Callsign string
}

type UserUsageStatistics struct {
	UserID                      int32
	PageViews                   int32
	SearchQueries               int32
	CodeIntelligenceActions     int32
	FindReferencesActions       int32
	LastActiveTime              *time.Time
	LastCodeHostIntegrationTime *time.Time
}

type SiteUsageStatistics struct {
	DAUs []*SiteActivityPeriod
	WAUs []*SiteActivityPeriod
	MAUs []*SiteActivityPeriod
}

type SiteActivityPeriod struct {
	StartTime            time.Time
	UserCount            int32
	RegisteredUserCount  int32
	AnonymousUserCount   int32
	IntegrationUserCount int32
	Stages               *Stages
}

type Stages struct {
	Manage    int32 `json:"mng"`
	Plan      int32 `json:"plan"`
	Code      int32 `json:"code"`
	Review    int32 `json:"rev"`
	Verify    int32 `json:"ver"`
	Package   int32 `json:"pkg"`
	Deploy    int32 `json:"depl"`
	Configure int32 `json:"conf"`
	Monitor   int32 `json:"mtr"`
	Secure    int32 `json:"sec"`
	Automate  int32 `json:"auto"`
}

type SurveyResponse struct {
	ID        int32
	UserID    *int32
	Email     *string
	Score     int32
	Reason    *string
	Better    *string
	CreatedAt time.Time
}
