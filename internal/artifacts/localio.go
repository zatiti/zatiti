package artifacts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"

	"github.com/zatiti/zatiti/internal/contract"
)

// contract.LocalIO seam for artifact.upload.chunk/finish/cancel, artifact.read
// and artifact.export. The registry routes exactly these operations through
// Prepare/Perform/Finish; Handle refuses them.
//
// Prepare always runs inside the caller's transaction: every DB read, every
// offset/replay/version decision and every pure-CPU integrity check (base64
// decode, chunk digest verification) happens here, because Perform runs
// outside any transaction with no Unit at all and cannot query storage.
// Perform does the only work that touches the blob store. Finish runs inside
// a fresh transaction: it revalidates the pinned versions and commits the
// terminal mutation or discloses the read.
//
// Chunk bytes are staged and immediately published under their own content
// digest as soon as they arrive, so a later artifact.upload.finish can open
// them back by digest (BlobStore only serves published content) and restage
// their concatenation as the one final, whole-file blob before publishing
// it under the upload's declared expected digest.

// maxChunkBytes bounds one decoded chunk (Wire conventions and limits:
// chunk 1 MiB decoded).
const maxChunkBytes = 1 << 20

// ---------- shared plan/result envelopes ----------

// chunkPlan is Prepare's sealed decision for artifact.upload.chunk.
type chunkPlan struct {
	Kind     string          `json:"kind"` // "replay" | "new"
	UploadID contract.ID     `json:"upload_id"`
	Offset   int64           `json:"offset"`
	Length   int64           `json:"length"`
	Digest   contract.Digest `json:"digest"`
}

// chunkResult is Perform's outcome for a new chunk.
type chunkResult struct {
	Kind   string          `json:"kind"`
	Digest contract.Digest `json:"digest,omitempty"`
	Length int64           `json:"length,omitempty"`
}

// finishChunkRef names one previously staged, published chunk to reassemble.
type finishChunkRef struct {
	Offset int64           `json:"offset"`
	Length int64           `json:"length"`
	Digest contract.Digest `json:"digest"`
}

// finishPlan is Prepare's sealed decision for artifact.upload.finish.
type finishPlan struct {
	UploadID       contract.ID      `json:"upload_id"`
	ExpectedDigest contract.Digest  `json:"expected_digest"`
	ExpectedSize   int64            `json:"expected_size"`
	MediaType      string           `json:"media_type"`
	Classification string           `json:"classification"`
	Chunks         []finishChunkRef `json:"chunks"`
}

// finishResult is Perform's outcome for artifact.upload.finish.
type finishResult struct {
	Digest contract.Digest `json:"digest"`
	Size   int64           `json:"size"`
}

// cancelPlan is Prepare's sealed decision for artifact.upload.cancel.
type cancelPlan struct {
	UploadID contract.ID `json:"upload_id"`
	Terminal bool        `json:"terminal"` // idempotent replay of an already-terminal upload
}

// readPlan is Prepare's sealed decision for artifact.read.
type readPlan struct {
	ArtifactID contract.ID     `json:"artifact_id"`
	Digest     contract.Digest `json:"digest"`
	Offset     int64           `json:"offset"`
	Length     int64           `json:"length"`
	TotalSize  int64           `json:"total_size"`
}

// readResult is Perform's outcome for artifact.read.
type readResult struct {
	BytesB64 string `json:"bytes_base64"`
}

// exportPlan is Prepare's sealed decision for artifact.export: the job it
// already created inside the admission transaction, plus the source
// artifact's coordinates so Perform can independently verify its bytes are
// actually intact before the export job is allowed to stand.
type exportPlan struct {
	Job        wireJob         `json:"job"`
	ArtifactID contract.ID     `json:"artifact_id"`
	Digest     contract.Digest `json:"digest"`
	Size       int64           `json:"size"`
}

// decodePlan decodes an IOPlan.Prepared or IOResult.Data payload.
func decodePlan[T any](raw json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		var zero T
		return zero, faultWrap(internalError("local IO plan payload is malformed"), err)
	}
	return v, nil
}

// Prepare implements contract.LocalIO.
func (s *Service) Prepare(ctx context.Context, unit contract.Unit, invocation contract.Invocation) (contract.IOPlan, error) {
	switch invocation.Operation {
	case opUploadChunk:
		return s.prepareChunk(ctx, unit, invocation)
	case opUploadFinish:
		return s.prepareFinish(ctx, unit, invocation)
	case opUploadCancel:
		return s.prepareCancel(ctx, unit, invocation)
	case opArtifactRead:
		return s.prepareRead(ctx, unit, invocation)
	case opArtifactExport:
		return s.prepareExport(ctx, unit, invocation)
	default:
		return contract.IOPlan{}, internalError(
			"operation %s does not route through the artifacts local IO seam", invocation.Operation)
	}
}

// Perform implements contract.LocalIO. It runs outside transactions with no
// Unit at all; the only permitted work is blob store IO against the exact
// trusted plan sealed by Prepare.
func (s *Service) Perform(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	switch plan.Invocation.Operation {
	case opUploadChunk:
		return s.performChunk(ctx, plan)
	case opUploadFinish:
		return s.performFinish(ctx, plan)
	case opUploadCancel:
		return contract.IOResult{Data: nil}, nil
	case opArtifactRead:
		return s.performRead(ctx, plan)
	case opArtifactExport:
		return s.performExport(ctx, plan)
	default:
		return contract.IOResult{}, internalError(
			"operation %s does not route through the artifacts local IO seam", plan.Invocation.Operation)
	}
}

// Finish implements contract.LocalIO. It revalidates authority and the
// pinned versions inside the completion transaction, then commits the
// terminal mutation or discloses the read.
func (s *Service) Finish(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	if plan.Owner != ownerName {
		return contract.Payload{}, permissionDenied("local IO plan does not belong to the artifacts owner")
	}
	switch plan.Invocation.Operation {
	case opUploadChunk:
		return s.finishChunk(ctx, unit, plan, result)
	case opUploadFinish:
		return s.finishUploadFinish(ctx, unit, plan, result)
	case opUploadCancel:
		return s.finishCancel(ctx, unit, plan, result)
	case opArtifactRead:
		return s.finishRead(ctx, unit, plan, result)
	case opArtifactExport:
		return s.finishExport(ctx, unit, plan, result)
	default:
		return contract.Payload{}, internalError(
			"operation %s does not route through the artifacts local IO seam", plan.Invocation.Operation)
	}
}

// ---------- artifact.upload.chunk ----------

func (s *Service) prepareChunk(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[chunkInput](opUploadChunk, inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if unit.ReadOnly() {
		return contract.IOPlan{}, invalidInput("operation %s is a mutation and requires a write transaction", opUploadChunk)
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.IOPlan{}, err
	}
	u, err := loadUpload(ctx, unit, in.UploadID)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if err := checkInstallation(unit, u.InstallationID); err != nil {
		return contract.IOPlan{}, notFound("upload %s does not exist", in.UploadID)
	}
	if u.State != "open" {
		return contract.IOPlan{}, conflictFault("upload %s is %s, not open", u.ID, u.State)
	}
	if !s.deps.Clock.Now().Before(u.ExpiresAt) {
		return contract.IOPlan{}, prerequisiteMissing("upload %s expired at %s", u.ID, wireTime(u.ExpiresAt))
	}

	decoded, err := base64.StdEncoding.DecodeString(in.BytesB64)
	if err != nil {
		return contract.IOPlan{}, invalidInput("chunk bytes are not valid base64")
	}
	if len(decoded) == 0 {
		return contract.IOPlan{}, invalidInput("chunk must carry at least one byte")
	}
	if len(decoded) > maxChunkBytes {
		return contract.IOPlan{}, invalidInput("chunk exceeds the %d MiB decoded limit", maxChunkBytes>>20)
	}
	if contract.Hash(decoded) != in.ChunkDigest {
		return contract.IOPlan{}, invalidInput("chunk digest does not match the decoded bytes")
	}

	var p chunkPlan
	existing, err := loadChunk(ctx, unit, u.ID, in.Offset)
	if err != nil {
		return contract.IOPlan{}, err
	}
	switch {
	case existing != nil:
		if existing.Digest != in.ChunkDigest || existing.Length != int64(len(decoded)) {
			return contract.IOPlan{}, conflictFault(
				"chunk at offset %d was already received with different content", in.Offset)
		}
		p = chunkPlan{Kind: "replay", UploadID: u.ID, Offset: in.Offset, Length: existing.Length, Digest: existing.Digest}
	case in.Offset != u.ReceivedSize:
		return contract.IOPlan{}, invalidInput(
			"chunk offset %d does not match the next contiguous offset %d", in.Offset, u.ReceivedSize)
	default:
		if u.ReceivedSize+int64(len(decoded)) > u.ExpectedSize {
			return contract.IOPlan{}, invalidInput("chunk would exceed the upload's expected size")
		}
		p = chunkPlan{Kind: "new", UploadID: u.ID, Offset: in.Offset, Length: int64(len(decoded)), Digest: in.ChunkDigest}
	}
	prepared, err := marshalJSON(p)
	if err != nil {
		return contract.IOPlan{}, err
	}
	return contract.IOPlan{
		ID:         s.deps.IDs.New(),
		Owner:      ownerName,
		Invocation: inv,
		Actor:      unit.Actor(),
		Scope:      unit.Scope(),
		Generation: unit.Generation(),
		ExpectedVersions: map[contract.ID]contract.Version{
			u.ID: u.Version,
		},
		Prepared: prepared,
	}, nil
}

func (s *Service) performChunk(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	p, err := decodePlan[chunkPlan](plan.Prepared)
	if err != nil {
		return contract.IOResult{}, err
	}
	if p.Kind == "replay" {
		raw, err := marshalJSON(chunkResult{Kind: "replay"})
		if err != nil {
			return contract.IOResult{}, err
		}
		return contract.IOResult{Data: raw}, nil
	}
	in, err := decodeInto[chunkInput](opUploadChunk, plan.Invocation.Input)
	if err != nil {
		return contract.IOResult{}, err
	}
	decoded, err := base64.StdEncoding.DecodeString(in.BytesB64)
	if err != nil {
		return contract.IOResult{}, err
	}
	ref, digest, size, err := s.deps.Blobs.Stage(ctx, bytes.NewReader(decoded), int64(len(decoded)))
	if err != nil {
		return contract.IOResult{Fault: blobFault(err)}, nil
	}
	if digest != p.Digest || size != p.Length {
		_ = s.deps.Blobs.RemoveStaged(ctx, ref)
		return contract.IOResult{Fault: artifactFault("blob store staged chunk does not match its declared digest")}, nil
	}
	if err := s.deps.Blobs.Publish(ctx, ref, digest); err != nil {
		return contract.IOResult{Fault: blobFault(err)}, nil
	}
	raw, err := marshalJSON(chunkResult{Kind: "new", Digest: digest, Length: size})
	if err != nil {
		return contract.IOResult{}, err
	}
	return contract.IOResult{Data: raw}, nil
}

func (s *Service) finishChunk(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	p, err := decodePlan[chunkPlan](plan.Prepared)
	if err != nil {
		return contract.Payload{}, err
	}
	u, err := loadUpload(ctx, unit, p.UploadID)
	if err != nil {
		return contract.Payload{}, err
	}
	if want, ok := plan.ExpectedVersions[u.ID]; ok && want != u.Version {
		return contract.Payload{}, staleVersion(
			"upload %s changed concurrently while the chunk was staged", u.ID)
	}
	if result.Fault != nil {
		return contract.Payload{Status: contract.StatusFailed, Error: result.Fault}, nil
	}
	if p.Kind == "replay" {
		return s.completed(uploadOutput{Resource: u.wire()})
	}
	res, err := decodePlan[chunkResult](result.Data)
	if err != nil {
		return contract.Payload{}, err
	}
	now := s.deps.Clock.Now()
	if err := insertChunk(ctx, unit, &chunkRow{
		UploadID: u.ID, Offset: p.Offset, Length: res.Length, Digest: res.Digest, CreatedAt: now,
	}); err != nil {
		return contract.Payload{}, err
	}
	u.ReceivedSize += res.Length
	u.UpdatedAt = now
	if err := updateUpload(ctx, unit, u); err != nil {
		return contract.Payload{}, err
	}
	data, err := marshalData(u.wire())
	if err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventUploadChunked, u.ID, u.Version, data); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(uploadOutput{Resource: u.wire()})
}

// ---------- artifact.upload.finish ----------

func (s *Service) prepareFinish(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[uploadRefInput](opUploadFinish, inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if unit.ReadOnly() {
		return contract.IOPlan{}, invalidInput("operation %s is a mutation and requires a write transaction", opUploadFinish)
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.IOPlan{}, err
	}
	u, err := loadUpload(ctx, unit, in.UploadID)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if err := checkInstallation(unit, u.InstallationID); err != nil {
		return contract.IOPlan{}, notFound("upload %s does not exist", in.UploadID)
	}
	if u.State != "open" {
		return contract.IOPlan{}, conflictFault("upload %s is %s, not open", u.ID, u.State)
	}
	if u.Version != contract.Version(in.ExpectedVersion) {
		return contract.IOPlan{}, staleVersion("upload %s version %d does not match expected version %d",
			u.ID, u.Version, in.ExpectedVersion)
	}
	if u.ReceivedSize != u.ExpectedSize {
		return contract.IOPlan{}, prerequisiteMissing(
			"upload %s has received %d of %d expected bytes", u.ID, u.ReceivedSize, u.ExpectedSize)
	}
	chunks, err := listChunks(ctx, unit, u.ID)
	if err != nil {
		return contract.IOPlan{}, err
	}
	refs := make([]finishChunkRef, 0, len(chunks))
	var next int64
	for _, c := range chunks {
		if c.Offset != next {
			return contract.IOPlan{}, internalError("upload %s has a gap in its received chunks", u.ID)
		}
		refs = append(refs, finishChunkRef{Offset: c.Offset, Length: c.Length, Digest: c.Digest})
		next += c.Length
	}
	if next != u.ExpectedSize {
		return contract.IOPlan{}, prerequisiteMissing("upload %s has received %d of %d expected bytes", u.ID, next, u.ExpectedSize)
	}
	prepared, err := marshalJSON(finishPlan{
		UploadID: u.ID, ExpectedDigest: u.ExpectedDigest, ExpectedSize: u.ExpectedSize,
		MediaType: u.MediaType, Classification: u.Classification, Chunks: refs,
	})
	if err != nil {
		return contract.IOPlan{}, err
	}
	return contract.IOPlan{
		ID:               s.deps.IDs.New(),
		Owner:            ownerName,
		Invocation:       inv,
		Actor:            unit.Actor(),
		Scope:            unit.Scope(),
		Generation:       unit.Generation(),
		ExpectedVersions: map[contract.ID]contract.Version{u.ID: u.Version},
		Prepared:         prepared,
	}, nil
}

// chunkReader concatenates every chunk's published bytes in offset order.
type chunkReader struct {
	ctx    context.Context
	blobs  contract.BlobStore
	chunks []finishChunkRef
	cur    io.ReadCloser
}

func (r *chunkReader) Read(p []byte) (int, error) {
	for {
		if r.cur == nil {
			if len(r.chunks) == 0 {
				return 0, io.EOF
			}
			c := r.chunks[0]
			r.chunks = r.chunks[1:]
			rc, err := r.blobs.Open(r.ctx, c.Digest, 0, c.Length)
			if err != nil {
				return 0, err
			}
			r.cur = rc
		}
		n, err := r.cur.Read(p)
		if err == io.EOF {
			_ = r.cur.Close()
			r.cur = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (s *Service) performFinish(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	p, err := decodePlan[finishPlan](plan.Prepared)
	if err != nil {
		return contract.IOResult{}, err
	}
	reader := &chunkReader{ctx: ctx, blobs: s.deps.Blobs, chunks: p.Chunks}
	ref, digest, size, err := s.deps.Blobs.Stage(ctx, reader, p.ExpectedSize)
	if err != nil {
		return contract.IOResult{Fault: blobFault(err)}, nil
	}
	if size != p.ExpectedSize || digest != p.ExpectedDigest {
		_ = s.deps.Blobs.RemoveStaged(ctx, ref)
		return contract.IOResult{Fault: conflictFault(
			"assembled upload does not match its declared expected digest or size")}, nil
	}
	if err := s.deps.Blobs.Publish(ctx, ref, digest); err != nil {
		return contract.IOResult{Fault: blobFault(err)}, nil
	}
	raw, err := marshalJSON(finishResult{Digest: digest, Size: size})
	if err != nil {
		return contract.IOResult{}, err
	}
	return contract.IOResult{Data: raw}, nil
}

func (s *Service) finishUploadFinish(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	p, err := decodePlan[finishPlan](plan.Prepared)
	if err != nil {
		return contract.Payload{}, err
	}
	u, err := loadUpload(ctx, unit, p.UploadID)
	if err != nil {
		return contract.Payload{}, err
	}
	if want, ok := plan.ExpectedVersions[u.ID]; !ok || want != u.Version {
		return contract.Payload{}, staleVersion("upload %s changed concurrently while it was being finished", u.ID)
	}
	if u.State != "open" {
		return contract.Payload{}, conflictFault("upload %s is %s, not open", u.ID, u.State)
	}
	if result.Fault != nil {
		return contract.Payload{Status: contract.StatusFailed, Error: result.Fault}, nil
	}
	res, err := decodePlan[finishResult](result.Data)
	if err != nil {
		return contract.Payload{}, err
	}
	now := s.deps.Clock.Now()
	a := &artifactRow{
		ID:             s.deps.IDs.New(),
		Version:        1,
		InstallationID: u.InstallationID,
		OrganizationID: u.OrganizationID,
		ProjectID:      u.ProjectID,
		WorkerID:       u.WorkerID,
		TaskID:         u.TaskID,
		Scope:          u.Scope,
		Digest:         res.Digest,
		Size:           res.Size,
		MediaType:      p.MediaType,
		Classification: p.Classification,
		Encrypted:      true,
		State:          "available",
		CreatedAt:      now,
	}
	if err := insertArtifact(ctx, unit, a); err != nil {
		return contract.Payload{}, err
	}
	if err := insertPin(ctx, unit, s.deps.IDs.New(), a.ID, "upload", now); err != nil {
		return contract.Payload{}, err
	}
	u.State = "finished"
	u.ArtifactID = a.ID
	u.UpdatedAt = now
	if err := updateUpload(ctx, unit, u); err != nil {
		return contract.Payload{}, err
	}
	artifactData, err := marshalData(a.wire())
	if err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventArtifactPublished, a.ID, a.Version, artifactData); err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, eventUploadFinished, u.ID, u.Version, artifactData); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(artifactOutput{Resource: a.wire()})
}

// ---------- artifact.upload.cancel ----------

func (s *Service) prepareCancel(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[uploadRefInput](opUploadCancel, inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if unit.ReadOnly() {
		return contract.IOPlan{}, invalidInput("operation %s is a mutation and requires a write transaction", opUploadCancel)
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.IOPlan{}, err
	}
	u, err := loadUpload(ctx, unit, in.UploadID)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if err := checkInstallation(unit, u.InstallationID); err != nil {
		return contract.IOPlan{}, notFound("upload %s does not exist", in.UploadID)
	}
	terminal := u.State == "cancelled" || u.State == "expired"
	if !terminal {
		if u.State != "open" {
			return contract.IOPlan{}, conflictFault("upload %s is already %s", u.ID, u.State)
		}
		if u.Version != contract.Version(in.ExpectedVersion) {
			return contract.IOPlan{}, staleVersion("upload %s version %d does not match expected version %d",
				u.ID, u.Version, in.ExpectedVersion)
		}
	}
	prepared, err := marshalJSON(cancelPlan{UploadID: u.ID, Terminal: terminal})
	if err != nil {
		return contract.IOPlan{}, err
	}
	return contract.IOPlan{
		ID:               s.deps.IDs.New(),
		Owner:            ownerName,
		Invocation:       inv,
		Actor:            unit.Actor(),
		Scope:            unit.Scope(),
		Generation:       unit.Generation(),
		ExpectedVersions: map[contract.ID]contract.Version{u.ID: u.Version},
		Prepared:         prepared,
	}, nil
}

func (s *Service) finishCancel(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	p, err := decodePlan[cancelPlan](plan.Prepared)
	if err != nil {
		return contract.Payload{}, err
	}
	u, err := loadUpload(ctx, unit, p.UploadID)
	if err != nil {
		return contract.Payload{}, err
	}
	if result.Fault != nil {
		return contract.Payload{Status: contract.StatusFailed, Error: result.Fault}, nil
	}
	if p.Terminal || u.State != "open" {
		// Idempotent replay: the terminal row already stands.
		return s.completed(uploadCancelOutput{Resource: &wireDisposition{ID: u.ID, Version: u.Version, State: u.State}})
	}
	if want, ok := plan.ExpectedVersions[u.ID]; !ok || want != u.Version {
		return contract.Payload{}, staleVersion("upload %s changed concurrently while it was being cancelled", u.ID)
	}
	now := s.deps.Clock.Now()
	state := "cancelled"
	kind := eventUploadCancelled
	if !now.Before(u.ExpiresAt) {
		// A late cancel records the honest expired terminal state rather
		// than a false cancellation.
		state = "expired"
	}
	u.State = state
	u.UpdatedAt = now
	if err := updateUpload(ctx, unit, u); err != nil {
		return contract.Payload{}, err
	}
	data, err := marshalData(u.wire())
	if err != nil {
		return contract.Payload{}, err
	}
	if err := emitTransition(ctx, unit, kind, u.ID, u.Version, data); err != nil {
		return contract.Payload{}, err
	}
	return s.completed(uploadCancelOutput{Resource: &wireDisposition{ID: u.ID, Version: u.Version, State: u.State}})
}

// ---------- artifact.read ----------

func (s *Service) prepareRead(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[readInput](opArtifactRead, inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.IOPlan{}, err
	}
	a, err := loadArtifact(ctx, unit, in.ID)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if err := narrowScope(in.Scope.toContract(), a.OrganizationID, a.ProjectID, a.WorkerID, a.TaskID); err != nil {
		return contract.IOPlan{}, err
	}
	if a.State != "available" {
		return contract.IOPlan{}, artifactFault("artifact %s is not available", a.ID)
	}
	if in.Offset > a.Size {
		return contract.IOPlan{}, invalidInput("offset %d exceeds artifact size %d", in.Offset, a.Size)
	}
	length := in.Length
	if remaining := a.Size - in.Offset; length > remaining {
		length = remaining
	}
	prepared, err := marshalJSON(readPlan{ArtifactID: a.ID, Digest: a.Digest, Offset: in.Offset, Length: length, TotalSize: a.Size})
	if err != nil {
		return contract.IOPlan{}, err
	}
	return contract.IOPlan{
		ID:         s.deps.IDs.New(),
		Owner:      ownerName,
		Invocation: inv,
		Actor:      unit.Actor(),
		Scope:      unit.Scope(),
		Generation: unit.Generation(),
		Prepared:   prepared,
	}, nil
}

func (s *Service) performRead(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	p, err := decodePlan[readPlan](plan.Prepared)
	if err != nil {
		return contract.IOResult{}, err
	}
	if p.Length == 0 {
		raw, err := marshalJSON(readResult{BytesB64: ""})
		if err != nil {
			return contract.IOResult{}, err
		}
		return contract.IOResult{Data: raw}, nil
	}
	rc, err := s.deps.Blobs.Open(ctx, p.Digest, p.Offset, p.Length)
	if err != nil {
		return contract.IOResult{Fault: blobFault(err)}, nil
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, p.Length))
	if err != nil {
		return contract.IOResult{Fault: blobFault(err)}, nil
	}
	raw, err := marshalJSON(readResult{BytesB64: base64.StdEncoding.EncodeToString(data)})
	if err != nil {
		return contract.IOResult{}, err
	}
	return contract.IOResult{Data: raw}, nil
}

func (s *Service) finishRead(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	p, err := decodePlan[readPlan](plan.Prepared)
	if err != nil {
		return contract.Payload{}, err
	}
	// Final authorization check before any bytes reach the caller: the
	// artifact must still exist, still be in scope and still be available.
	a, err := loadArtifact(ctx, unit, p.ArtifactID)
	if err != nil {
		return contract.Payload{}, err
	}
	if err := narrowScope(unit.Scope(), a.OrganizationID, a.ProjectID, a.WorkerID, a.TaskID); err != nil {
		return contract.Payload{}, err
	}
	if a.State != "available" {
		return contract.Payload{}, artifactFault("artifact %s is not available", a.ID)
	}
	if result.Fault != nil {
		return contract.Payload{Status: contract.StatusFailed, Error: result.Fault}, nil
	}
	res, err := decodePlan[readResult](result.Data)
	if err != nil {
		return contract.Payload{}, err
	}
	return s.completed(readOutput{
		BytesB64:  res.BytesB64,
		Digest:    a.Digest,
		Offset:    p.Offset,
		TotalSize: p.TotalSize,
	})
}

// ---------- artifact.export ----------

func (s *Service) prepareExport(ctx context.Context, unit contract.Unit, inv contract.Invocation) (contract.IOPlan, error) {
	in, err := decodeInto[exportInput](opArtifactExport, inv.Input)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if unit.ReadOnly() {
		return contract.IOPlan{}, invalidInput("operation %s is a mutation and requires a write transaction", opArtifactExport)
	}
	if err := checkInstallation(unit, in.Scope.InstallationID); err != nil {
		return contract.IOPlan{}, err
	}
	a, err := loadArtifact(ctx, unit, in.ID)
	if err != nil {
		return contract.IOPlan{}, err
	}
	if err := narrowScope(in.Scope.toContract(), a.OrganizationID, a.ProjectID, a.WorkerID, a.TaskID); err != nil {
		return contract.IOPlan{}, err
	}
	if a.State != "available" {
		return contract.IOPlan{}, artifactFault("artifact %s is not available", a.ID)
	}
	job, err := s.executionJobCreate(ctx, unit, executionJobCreateInput{
		Scope:     in.Scope,
		Owner:     ownerName,
		Operation: opArtifactExport,
		Input:     inv.Input,
		SourceID:  s.deps.IDs.New(),
	})
	if err != nil {
		return contract.IOPlan{}, err
	}
	prepared, err := marshalJSON(exportPlan{Job: job, ArtifactID: a.ID, Digest: a.Digest, Size: a.Size})
	if err != nil {
		return contract.IOPlan{}, err
	}
	return contract.IOPlan{
		ID:         s.deps.IDs.New(),
		Owner:      ownerName,
		Invocation: inv,
		Actor:      unit.Actor(),
		Scope:      unit.Scope(),
		Generation: unit.Generation(),
		Prepared:   prepared,
	}, nil
}

// performExport independently verifies the source artifact's bytes are
// actually intact before the export job it wraps is allowed to stand: a
// legitimate export must not certify content that has already gone missing
// or corrupt underneath its committed metadata.
func (s *Service) performExport(ctx context.Context, plan contract.IOPlan) (contract.IOResult, error) {
	p, err := decodePlan[exportPlan](plan.Prepared)
	if err != nil {
		return contract.IOResult{}, err
	}
	rc, err := s.deps.Blobs.Open(ctx, p.Digest, 0, p.Size)
	if err != nil {
		return contract.IOResult{Fault: blobFault(err)}, nil
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, p.Size+1))
	if err != nil {
		return contract.IOResult{Fault: blobFault(err)}, nil
	}
	if int64(len(data)) != p.Size || contract.Hash(data) != p.Digest {
		return contract.IOResult{Fault: artifactFault(
			"artifact %s bytes no longer match their committed digest", p.ArtifactID)}, nil
	}
	return contract.IOResult{Data: nil}, nil
}

func (s *Service) finishExport(ctx context.Context, unit contract.Unit, plan contract.IOPlan, result contract.IOResult) (contract.Payload, error) {
	p, err := decodePlan[exportPlan](plan.Prepared)
	if err != nil {
		return contract.Payload{}, err
	}
	if result.Fault != nil {
		a, lerr := loadArtifact(ctx, unit, p.ArtifactID)
		if lerr != nil {
			return contract.Payload{}, lerr
		}
		if err := s.markArtifactFault(ctx, unit, a, result.Fault.Code, result.Fault.Message); err != nil {
			return contract.Payload{}, err
		}
		return contract.Payload{Status: contract.StatusFailed, Error: result.Fault}, nil
	}
	raw, err := marshalData(jobOutput{Resource: p.Job})
	if err != nil {
		return contract.Payload{}, err
	}
	return contract.Payload{Status: contract.StatusAccepted, Data: raw}, nil
}
