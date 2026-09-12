package artifacts

import "time"

// wireTime renders a UTC RFC3339Nano wire timestamp, matching the shared
// UTC $defs pattern (a trailing "Z").
func wireTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// wire projects a stored artifact onto its wire shape.
func (a *artifactRow) wire() wireArtifact {
	return wireArtifact{
		ID:      a.ID,
		Version: a.Version,
		Scope: wireScope{
			InstallationID: a.InstallationID,
			OrganizationID: a.OrganizationID,
			ProjectID:      a.ProjectID,
			WorkerID:       a.WorkerID,
			TaskID:         a.TaskID,
		},
		Digest:         a.Digest,
		Size:           a.Size,
		MediaType:      a.MediaType,
		Classification: a.Classification,
		Encrypted:      a.Encrypted,
		State:          a.State,
		CreatedAt:      wireTime(a.CreatedAt),
	}
}

// wire projects a stored upload onto its wire shape.
func (u *uploadRow) wire() wireUpload {
	return wireUpload{
		ID:      u.ID,
		Version: u.Version,
		Scope: wireScope{
			InstallationID: u.InstallationID,
			OrganizationID: u.OrganizationID,
			ProjectID:      u.ProjectID,
			WorkerID:       u.WorkerID,
			TaskID:         u.TaskID,
		},
		ExpectedSize:   u.ExpectedSize,
		ExpectedDigest: u.ExpectedDigest,
		ReceivedSize:   u.ReceivedSize,
		ExpiresAt:      wireTime(u.ExpiresAt),
		State:          u.State,
	}
}
