package skills

import (
	"context"
	"encoding/json"

	"github.com/zatiti/zatiti/internal/contract"
)

// Event emission helpers. Every event appends atomically with the state
// change it describes, in the caller's transaction, through unit.Emit.
// Kind follows the owner.entity.transition convention.

// emitSkillImported appends the skill.imported event for a freshly staged
// immutable version.
func (s *Service) emitSkillImported(ctx context.Context, unit contract.Unit, id contract.ID, version int64, name string, digest contract.Digest) error {
	data, err := marshalJSON(map[string]any{
		"type":           "skills.skill.imported",
		"name":           name,
		"content_digest": digest,
	})
	if err != nil {
		return err
	}
	return unit.Emit(ctx, contract.Event{
		Kind:            "skills.skill.imported",
		ResourceID:      id,
		ResourceVersion: contract.Version(version),
		Data:            json.RawMessage(data),
	})
}

// emitSkillState appends the activated or archived event for one version
// transition driven by _skills.activate.
func (s *Service) emitSkillState(ctx context.Context, unit contract.Unit, kind string, id contract.ID, version int64, name string) error {
	data, err := marshalJSON(map[string]any{
		"type": kind,
		"name": name,
	})
	if err != nil {
		return err
	}
	return unit.Emit(ctx, contract.Event{
		Kind:            kind,
		ResourceID:      id,
		ResourceVersion: contract.Version(version),
		Data:            json.RawMessage(data),
	})
}

// emitEvaluationCreated appends the evaluation.created event for a newly
// sealed evaluation job.
func (s *Service) emitEvaluationCreated(ctx context.Context, unit contract.Unit, id contract.ID, version int64, skillID contract.ID, skillVersion int64) error {
	data, err := marshalJSON(map[string]any{
		"type":          "skills.evaluation.created",
		"skill_id":      skillID,
		"skill_version": skillVersion,
	})
	if err != nil {
		return err
	}
	return unit.Emit(ctx, contract.Event{
		Kind:            "skills.evaluation.created",
		ResourceID:      id,
		ResourceVersion: contract.Version(version),
		Data:            json.RawMessage(data),
	})
}
