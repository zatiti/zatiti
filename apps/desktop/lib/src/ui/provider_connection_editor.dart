// Adds a provider connection through the public compiler workflow. Credential
// material is entered later through the signed native helper.

import 'package:flutter/material.dart';

import '../api/models.dart' as wire;
import '../state/live_source.dart';
import '../state/workspace_controller.dart';
import '../state/workspace_source.dart';
import 'theme.dart';

Future<bool?> showProviderConnectionEditor(
  BuildContext context, {
  required WorkspaceController controller,
}) => showDialog<bool>(
  context: context,
  barrierLabel: 'Close provider setup',
  builder: (_) => _ProviderConnectionEditor(controller: controller),
);

class _ProviderConnectionEditor extends StatefulWidget {
  const _ProviderConnectionEditor({required this.controller});
  final WorkspaceController controller;

  @override
  State<_ProviderConnectionEditor> createState() =>
      _ProviderConnectionEditorState();
}

class _ProviderConnectionEditorState extends State<_ProviderConnectionEditor> {
  late final Future<List<wire.ProviderDescriptor>> _providers;
  final TextEditingController _account = TextEditingController();
  wire.ProviderDescriptor? _provider;
  String? _status;
  bool _busy = false;
  bool _unknown = false;
  PendingSubmission? _pending;
  String? _step;
  ResourceSubmission<DraftedResource>? _stage;
  ResourceSubmission<PlanOutcome>? _plan;

  @override
  void initState() {
    super.initState();
    final source = widget.controller.source;
    _providers = source is LiveWorkspaceSource
        ? source.api.modelProviders()
        : Future.value(const []);
  }

  @override
  void dispose() {
    _account.dispose();
    super.dispose();
  }

  Future<bool> _send(
    PendingSubmission submission,
    String step,
    LiveWorkspaceSource source,
  ) async {
    try {
      await source.submit(submission);
      _pending = null;
      _step = null;
      _unknown = false;
      return true;
    } on AcknowledgmentUnknown {
      setState(() {
        _busy = false;
        _unknown = true;
        _pending = submission;
        _step = step;
        _status =
            'The controller acknowledgment is unknown. Check its status before retrying.';
      });
      return false;
    } on Exception catch (e) {
      _failed(e);
      return false;
    }
  }

  Future<void> _create(LiveWorkspaceSource source) async {
    final provider = _provider;
    if (provider == null) {
      setState(() => _status = 'Choose a provider.');
      return;
    }
    if (_account.text.trim().isEmpty) {
      setState(() => _status = 'Enter the expected provider account identity.');
      return;
    }
    setState(() {
      _busy = true;
      _unknown = false;
      _stage = null;
      _plan = null;
      _status = 'Staging provider connection…';
    });
    try {
      _stage = source.prepareProviderConnection(
        provider: provider,
        accountIdentity: _account.text,
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
      setState(() => _status = 'Checking provider connection setup…');
      if (!await _send(_plan!, 'plan', source)) return;
      setState(() {
        _busy = false;
        _status = _plan!.result!.isClean
            ? 'Review this provider and expected account, then apply the plan.'
            : 'The controller blocked this connection. Review the plan details below.';
      });
    } on Exception catch (e) {
      _failed(e);
    }
  }

  Future<void> _apply(LiveWorkspaceSource source) async {
    final plan = _plan?.result;
    if (plan == null || !plan.isClean) return;
    setState(() {
      _busy = true;
      _status = 'Applying the reviewed connection plan…';
    });
    try {
      final submission = source.prepareApplyPlan(plan);
      if (!await _send(submission, 'apply', source)) return;
      await widget.controller.reconnect();
      if (mounted) Navigator.of(context).pop(true);
    } on Exception catch (e) {
      _failed(e);
    }
  }

  Future<void> _checkStatus(LiveWorkspaceSource source) async {
    final pending = _pending;
    final step = _step;
    if (pending == null || step == null) return;
    setState(() {
      _busy = true;
      _status = 'Checking the saved command…';
    });
    try {
      switch (await source.resolve(pending)) {
        case ResolvedAcknowledged():
          _pending = null;
          _step = null;
          _unknown = false;
          if (step == 'stage') {
            await _planDraft(source);
          } else if (step == 'plan') {
            setState(() {
              _busy = false;
              _status = _plan?.result?.isClean == true
                  ? 'Review this provider and expected account, then apply the plan.'
                  : 'The controller blocked this connection. Review the plan details below.';
            });
          } else {
            await widget.controller.reconnect();
            if (mounted) Navigator.of(context).pop(true);
          }
          break;
        case ResolvedRefused(:final refusal):
          _pending = null;
          _step = null;
          _failed(refusal);
          break;
        case ResolvedNotReceived():
          setState(() {
            _busy = false;
            _unknown = false;
            _status =
                'The controller did not receive this step. Close and retry setup.';
          });
          break;
      }
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
          : 'Provider connection could not be saved: $error';
    });
  }

  @override
  Widget build(BuildContext context) {
    final source = widget.controller.source;
    if (source is! LiveWorkspaceSource) {
      return const AlertDialog(
        title: Text('Add provider'),
        content: Text('Provider setup requires a live controller.'),
      );
    }
    return AlertDialog(
      title: const Text('Add provider connection'),
      content: SizedBox(
        width: 480,
        child: FutureBuilder<List<wire.ProviderDescriptor>>(
          future: _providers,
          builder: (context, snapshot) {
            if (snapshot.hasError) {
              return const Text('Provider choices could not be loaded.');
            }
            if (!snapshot.hasData) return const LinearProgressIndicator();
            final providers = snapshot.data!;
            return Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                DropdownButtonFormField<String>(
                  value: _provider?.id,
                  decoration: const InputDecoration(labelText: 'Provider'),
                  items: [
                    for (final p in providers)
                      DropdownMenuItem(value: p.id, child: Text(p.displayName)),
                  ],
                  onChanged: _busy
                      ? null
                      : (id) => setState(() {
                          _provider = providers
                              .where((p) => p.id == id)
                              .firstOrNull;
                        }),
                ),
                if (_provider != null) ...[
                  const SizedBox(height: Space.sm),
                  Text(
                    '${_provider!.defaultEndpoint} · ${_provider!.sessionMode}',
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                ],
                const SizedBox(height: Space.md),
                TextField(
                  controller: _account,
                  enabled: !_busy,
                  decoration: const InputDecoration(
                    labelText: 'Expected provider account identity',
                    helperText:
                        'The controller checks this against the account observed during key validation.',
                  ),
                ),
                const SizedBox(height: Space.md),
                const Text(
                  'The API key is not entered here. After applying the connection, use Set API key to open the signed local credential helper.',
                ),
                if (_status != null) ...[
                  const SizedBox(height: Space.md),
                  Text(_status!),
                ],
                if (_plan?.result != null) ...[
                  for (final message in _plan!.result!.diagnostics)
                    Text(message),
                  for (final message in _plan!.result!.pendingRequirements)
                    Text(message),
                ],
                if (_unknown)
                  TextButton(
                    onPressed: _busy ? null : () => _checkStatus(source),
                    child: const Text('Check save status'),
                  ),
              ],
            );
          },
        ),
      ),
      actions: [
        TextButton(
          onPressed: _busy ? null : () => Navigator.of(context).pop(false),
          child: const Text('Cancel'),
        ),
        if (_plan?.result?.isClean == true)
          FilledButton(
            onPressed: _busy ? null : () => _apply(source),
            child: Text(_busy ? 'Saving…' : 'Apply connection'),
          )
        else
          FilledButton(
            onPressed: _busy || _provider == null || _plan?.result != null
                ? null
                : () => _create(source),
            child: Text(_busy ? 'Saving…' : 'Review connection'),
          ),
      ],
    );
  }
}
