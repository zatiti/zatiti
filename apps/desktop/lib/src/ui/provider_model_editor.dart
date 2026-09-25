// Assigns one controller-owned execution profile through the normal
// configuration draft → plan → apply path. The editor cannot author profile
// evidence or dispatch JSON.

import 'package:flutter/material.dart';

import '../api/models.dart' as wire;
import '../state/live_source.dart';
import '../state/workspace_controller.dart';
import '../state/workspace_source.dart';
import 'theme.dart';

Future<void> showProviderModelEditor(
  BuildContext context, {
  required WorkspaceController controller,
  required String workerId,
}) => showDialog<void>(
  context: context,
  barrierLabel: 'Close model selection',
  builder: (_) =>
      _ProviderModelEditor(controller: controller, workerId: workerId),
);

class _ProviderModelEditor extends StatefulWidget {
  const _ProviderModelEditor({
    required this.controller,
    required this.workerId,
  });

  final WorkspaceController controller;
  final String workerId;

  @override
  State<_ProviderModelEditor> createState() => _ProviderModelEditorState();
}

class _ProviderModelEditorState extends State<_ProviderModelEditor> {
  Future<List<wire.ExecutionProfile>>? _profiles;
  List<wire.ExecutionProfile> _available = const [];
  String? _selected;
  String? _active;
  String? _status;
  bool _busy = false;
  bool _unknown = false;
  PendingSubmission? _pending;
  String? _pendingStep;
  ResourceSubmission<DraftedResource>? _stage;
  ResourceSubmission<PlanOutcome>? _plan;

  @override
  void initState() {
    super.initState();
    final source = widget.controller.source;
    if (source is LiveWorkspaceSource) {
      _profiles = source.api.executionProfiles();
      _profiles!.then<void>(
        (items) {
          if (mounted) setState(() => _available = items);
        },
        onError: (Object error, StackTrace stack) {
          if (mounted) {
            setState(() => _status = 'Saved profiles could not be loaded.');
          }
        },
      );
      source.api
          .workerGet(widget.workerId)
          .then((w) {
            if (!mounted) return;
            setState(() {
              _active = w.profile == null
                  ? null
                  : '${w.profile!.id}@${w.profile!.version}';
              _selected ??= _active;
            });
          })
          .catchError((Object error) {
            if (mounted) {
              setState(
                () =>
                    _status = 'The active worker profile could not be loaded.',
              );
            }
          });
    }
  }

  String _identity(wire.ExecutionProfile profile) =>
      '${profile.id}@${profile.version}';

  Future<void> _selectAndStage(
    wire.ExecutionProfile profile,
    LiveWorkspaceSource source,
  ) async {
    setState(() {
      _busy = true;
      _status = 'Saving a controller draft…';
      _unknown = false;
      _stage = null;
      _plan = null;
    });
    try {
      _stage = await source.prepareWorkerProfileUpdate(
        workerId: widget.workerId,
        profile: profile,
      );
      if (!await _send(_stage!, 'stage', source)) return;
      await _planDraft(source);
    } on Exception catch (e) {
      _failed(e);
    }
  }

  Future<void> _planDraft(LiveWorkspaceSource source) async {
    final draft = _stage?.result;
    if (draft == null) return;
    try {
      _plan = source.preparePlan(
        draftId: draft.draftId,
        expectedVersion: draft.draftVersion,
      );
      setState(() => _status = 'Validating the proposed profile assignment…');
      if (!await _send(_plan!, 'plan', source)) return;
      final result = _plan!.result!;
      setState(() {
        _busy = false;
        _status = result.isClean
            ? 'The controller plan is ready. Review it, then apply to use this profile on the next turn.'
            : 'The controller blocked this profile assignment. Review its diagnostics and requirements below.';
      });
    } on Exception catch (e) {
      _failed(e);
    }
  }

  Future<bool> _send(
    PendingSubmission submission,
    String step,
    LiveWorkspaceSource source,
  ) async {
    try {
      await source.submit(submission);
      _pending = null;
      _pendingStep = null;
      return true;
    } on AcknowledgmentUnknown {
      setState(() {
        _busy = false;
        _unknown = true;
        _pending = submission;
        _pendingStep = step;
        _status =
            'The controller acknowledgment is unknown. Check its status before retrying.';
      });
      return false;
    } on Exception catch (e) {
      _failed(e);
      return false;
    }
  }

  Future<void> _checkStatus(LiveWorkspaceSource source) async {
    final pending = _pending;
    final step = _pendingStep;
    if (pending == null || step == null) return;
    setState(() {
      _busy = true;
      _status = 'Checking the saved command…';
    });
    try {
      switch (await source.resolve(pending)) {
        case ResolvedAcknowledged():
          _pending = null;
          _pendingStep = null;
          _unknown = false;
          if (step == 'stage') {
            await _planDraft(source);
          } else if (step == 'plan') {
            final plan = _plan?.result;
            setState(() {
              _busy = false;
              _status = plan?.isClean == true
                  ? 'The controller plan is ready. Review it, then apply to use this profile on the next turn.'
                  : 'The controller blocked this profile assignment. Review its diagnostics and requirements below.';
            });
          } else {
            await widget.controller.reconnect();
            if (mounted) Navigator.of(context).pop();
          }
          break;
        case ResolvedRefused(:final refusal):
          _pending = null;
          _pendingStep = null;
          _failed(refusal);
          break;
        case ResolvedNotReceived():
          setState(() {
            _busy = false;
            _unknown = false;
            _status =
                'The controller did not receive this step. Retry the same saved command to continue.';
          });
          break;
      }
    } on Exception catch (e) {
      _failed(e);
    }
  }

  Future<void> _retry(LiveWorkspaceSource source) async {
    final pending = _pending;
    final step = _pendingStep;
    if (pending == null || step == null) return;
    setState(() {
      _busy = true;
      _unknown = false;
      _status = 'Retrying the same command…';
    });
    if (!await _send(pending, step, source)) return;
    if (step == 'stage') {
      await _planDraft(source);
    } else if (step == 'plan') {
      final plan = _plan?.result;
      setState(() {
        _busy = false;
        _status = plan?.isClean == true
            ? 'The controller plan is ready. Review it, then apply to use this profile on the next turn.'
            : 'The controller blocked this profile assignment. Review its diagnostics and requirements below.';
      });
    } else {
      await widget.controller.reconnect();
      if (mounted) Navigator.of(context).pop();
    }
  }

  Future<void> _apply(LiveWorkspaceSource source) async {
    final plan = _plan?.result;
    if (plan == null || !plan.isClean) return;
    setState(() {
      _busy = true;
      _status = 'Applying the reviewed controller plan…';
    });
    try {
      final submission = source.prepareApplyPlan(plan);
      if (!await _send(submission, 'apply', source)) return;
      await widget.controller.reconnect();
      if (mounted) Navigator.of(context).pop();
    } on Exception catch (e) {
      _failed(e);
    }
  }

  void _failed(Object error) {
    if (!mounted) return;
    setState(() {
      _busy = false;
      _status = error is SourceRefusal
          ? '${error.kind == RefusalKind.stale ? 'Stale configuration' : 'Save refused'}: ${error.message}'
          : 'The profile could not be saved: $error';
    });
  }

  @override
  Widget build(BuildContext context) {
    final source = widget.controller.source;
    final text = Theme.of(context).textTheme;
    if (source is! LiveWorkspaceSource) {
      return const AlertDialog(
        title: Text('Model profile'),
        content: Text(
          'Model profiles are available only from a live controller.',
        ),
      );
    }
    return AlertDialog(
      title: const Text('Choose a model profile'),
      content: SizedBox(
        width: 480,
        child: FutureBuilder<List<wire.ExecutionProfile>>(
          future: _profiles,
          builder: (context, snapshot) {
            if (snapshot.hasError) {
              return const Text('Saved model profiles could not be loaded.');
            }
            if (!snapshot.hasData) return const LinearProgressIndicator();
            final profiles = snapshot.data!;
            if (profiles.isEmpty) {
              return const Text(
                'No saved model profiles are available. A profile must be created and qualified by the controller before a worker can use it.',
              );
            }
            final items = [
              for (final p in profiles)
                DropdownMenuItem(
                  value: _identity(p),
                  child: Text(
                    '${p.provider ?? 'Provider not reported'} · ${p.model}',
                  ),
                ),
            ];
            final selected = profiles
                .where((p) => _identity(p) == _selected)
                .firstOrNull;
            return Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (_active != null)
                  Padding(
                    padding: const EdgeInsets.only(bottom: Space.md),
                    child: Text(
                      'Active: ${profiles.where((p) => _identity(p) == _active).map((p) => '${p.provider ?? 'Provider not reported'} · ${p.model}').firstOrNull ?? 'a profile not in the current saved list'}',
                      style: text.bodySmall,
                    ),
                  ),
                DropdownButtonFormField<String>(
                  value: selected == null ? null : _selected,
                  decoration: const InputDecoration(labelText: 'Saved profile'),
                  items: items,
                  onChanged: _busy
                      ? null
                      : (value) => setState(() => _selected = value),
                ),
                if (selected != null) ...[
                  const SizedBox(height: Space.md),
                  Text(
                    '${selected.contextCapture} context capture · cost ${selected.costEnforcement ?? 'not reported'} · ${selected.costBound.format()}',
                    style: text.bodySmall,
                  ),
                ],
                if (_status != null) ...[
                  const SizedBox(height: Space.md),
                  Text(_status!, style: text.bodySmall),
                ],
                if (_plan?.result != null) ...[
                  for (final message in _plan!.result!.diagnostics)
                    Text(message, style: text.bodySmall),
                  for (final message in _plan!.result!.pendingRequirements)
                    Text(message, style: text.bodySmall),
                ],
                if (_unknown)
                  TextButton(
                    onPressed: _busy ? null : () => _checkStatus(source),
                    child: const Text('Check save status'),
                  ),
                if (_status?.startsWith('The controller did not receive') ==
                    true)
                  TextButton(
                    onPressed: _busy ? null : () => _retry(source),
                    child: const Text('Retry same command'),
                  ),
              ],
            );
          },
        ),
      ),
      actions: [
        TextButton(
          onPressed: _busy ? null : () => Navigator.of(context).pop(),
          child: const Text('Close'),
        ),
        if (_plan?.result?.isClean == true)
          FilledButton(
            onPressed: _busy ? null : () => _apply(source),
            child: Text(_busy ? 'Saving…' : 'Apply for next turn'),
          )
        else
          FilledButton(
            onPressed:
                _busy ||
                    _selected == null ||
                    _selected == _active ||
                    _plan?.result != null
                ? null
                : () {
                    final profile = _available
                        .where((p) => _identity(p) == _selected)
                        .firstOrNull;
                    if (profile != null) _selectAndStage(profile, source);
                  },
            child: Text(_busy ? 'Saving…' : 'Review save'),
          ),
      ],
    );
  }
}
