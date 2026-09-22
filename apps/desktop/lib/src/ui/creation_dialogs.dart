// New organization/new worker/new responsibility: the draft/plan/review/
// apply flow as one dialog shape shared by all three, since the flow itself
// (stage → plan → review the exact candidate → apply) is identical. New
// group is a separate, simpler dialog: group chats are immediate
// (`conversation.create`), never staged through the compiler.

import 'package:flutter/material.dart';

import '../state/snapshot.dart';
import '../state/view_state.dart';
import '../state/workspace_controller.dart';
import 'theme.dart';
import 'widgets.dart';

Future<void> showNewOrganizationDialog(
  BuildContext context,
  WorkspaceController controller,
) => showDialog<void>(
  context: context,
  barrierLabel: 'Close new organization',
  builder: (_) => _NewOrganizationDialog(controller: controller),
);

Future<void> showNewWorkerDialog(
  BuildContext context,
  WorkspaceController controller,
) => showDialog<void>(
  context: context,
  barrierLabel: 'Close new worker',
  builder: (_) => _NewWorkerDialog(controller: controller),
);

Future<void> showNewResponsibilityDialog(
  BuildContext context,
  WorkspaceController controller,
  WorkerId workerId,
) => showDialog<void>(
  context: context,
  barrierLabel: 'Close new responsibility',
  builder: (_) =>
      _NewResponsibilityDialog(controller: controller, workerId: workerId),
);

Future<void> showNewGroupDialog(
  BuildContext context,
  WorkspaceController controller,
) => showDialog<void>(
  context: context,
  barrierLabel: 'Close new group',
  builder: (_) => _NewGroupDialog(controller: controller),
);

/// The shared draft/plan/review/apply body: a compose form until the flow
/// starts, then a fixed progression a person can watch and, at [ready], must
/// explicitly approve before anything is real.
class _DraftFlowBody extends StatelessWidget {
  const _DraftFlowBody({
    required this.kind,
    required this.controller,
    required this.composer,
    required this.onDone,
  });

  final DraftFlowKind kind;
  final WorkspaceController controller;

  /// The form, shown only while [DraftFlowPhase.composing].
  final Widget composer;
  final VoidCallback onDone;

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    final view = controller.draftFlow(kind);
    switch (view.phase) {
      case DraftFlowPhase.composing:
        return composer;
      case DraftFlowPhase.staging:
      case DraftFlowPhase.planning:
      case DraftFlowPhase.applying:
        return const Padding(
          padding: EdgeInsets.symmetric(vertical: Space.xl),
          child: Center(child: CircularProgressIndicator()),
        );
      case DraftFlowPhase.stagingUnknown:
      case DraftFlowPhase.planningUnknown:
      case DraftFlowPhase.applyingUnknown:
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Notice(
              'Checking whether this was received. Nothing is resent.',
            ),
            const SizedBox(height: Space.md),
            OutlinedButton(
              key: const ValueKey('draft-flow-check'),
              onPressed: () => controller.checkDraftFlow(kind),
              child: const Text('Check again'),
            ),
          ],
        );
      case DraftFlowPhase.ready:
      case DraftFlowPhase.blocked:
        final plan = view.plan!;
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text('Review before this becomes real', style: text.titleMedium),
            const SizedBox(height: Space.md),
            if (plan.isClean)
              const Notice(
                'Nothing else is needed. Create it to activate now.',
                icon: Icons.check_circle_outline,
              )
            else ...[
              for (final d in plan.diagnostics)
                Padding(
                  padding: const EdgeInsets.only(bottom: Space.sm),
                  child: Notice(d, icon: Icons.error_outline),
                ),
              for (final r in plan.pendingRequirements)
                Padding(
                  padding: const EdgeInsets.only(bottom: Space.sm),
                  child: Notice(r, icon: Icons.shield_outlined),
                ),
            ],
            const SizedBox(height: Space.lg),
            Wrap(
              spacing: Space.sm,
              children: [
                OutlinedButton(
                  onPressed: () => controller.cancelDraftFlow(kind),
                  child: const Text('Cancel'),
                ),
                if (plan.isClean)
                  FilledButton(
                    key: const ValueKey('draft-flow-apply'),
                    onPressed: () => controller.applyDraftFlow(kind),
                    child: const Text('Create'),
                  ),
              ],
            ),
          ],
        );
      case DraftFlowPhase.applied:
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Notice(
              'Created and active.',
              icon: Icons.check_circle_outline,
            ),
            const SizedBox(height: Space.lg),
            Align(
              alignment: Alignment.centerRight,
              child: FilledButton(
                key: const ValueKey('draft-flow-done'),
                onPressed: onDone,
                child: const Text('Done'),
              ),
            ),
          ],
        );
      case DraftFlowPhase.failed:
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Notice(view.note ?? 'This was refused.', icon: Icons.block),
            const SizedBox(height: Space.lg),
            Wrap(
              spacing: Space.sm,
              children: [
                OutlinedButton(
                  onPressed: () => controller.cancelDraftFlow(kind),
                  child: const Text('Start over'),
                ),
                OutlinedButton(
                  key: const ValueKey('draft-flow-retry'),
                  onPressed: () => controller.retryDraftFlow(kind),
                  child: const Text('Try again'),
                ),
              ],
            ),
          ],
        );
    }
  }
}

class _DialogShell extends StatelessWidget {
  const _DialogShell({
    required this.title,
    required this.controller,
    required this.listenable,
    required this.builder,
  });

  final String title;
  final WorkspaceController controller;
  final Listenable listenable;
  final WidgetBuilder builder;

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    return ListenableBuilder(
      listenable: listenable,
      builder: (context, _) => Dialog(
        insetPadding: const EdgeInsets.all(Space.xl),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 480),
          child: Padding(
            padding: const EdgeInsets.all(28),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Expanded(
                      child: Semantics(
                        header: true,
                        namesRoute: true,
                        child: Text(title, style: text.titleLarge),
                      ),
                    ),
                    IconButton(
                      tooltip: 'Close',
                      onPressed: () => Navigator.of(context).pop(),
                      icon: const Icon(Icons.close),
                    ),
                  ],
                ),
                const SizedBox(height: Space.lg),
                builder(context),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _NewOrganizationDialog extends StatefulWidget {
  const _NewOrganizationDialog({required this.controller});
  final WorkspaceController controller;

  @override
  State<_NewOrganizationDialog> createState() => _NewOrganizationDialogState();
}

class _NewOrganizationDialogState extends State<_NewOrganizationDialog> {
  final _key = TextEditingController();
  final _name = TextEditingController();
  final _chiefName = TextEditingController();
  final _chiefPurpose = TextEditingController();

  @override
  void dispose() {
    _key.dispose();
    _name.dispose();
    _chiefName.dispose();
    _chiefPurpose.dispose();
    super.dispose();
  }

  bool get _canSubmit =>
      _key.text.trim().isNotEmpty &&
      _name.text.trim().isNotEmpty &&
      _chiefName.text.trim().isNotEmpty;

  @override
  Widget build(BuildContext context) => _DialogShell(
    title: 'New organization',
    controller: widget.controller,
    listenable: widget.controller,
    builder: (context) => _DraftFlowBody(
      kind: DraftFlowKind.organization,
      controller: widget.controller,
      onDone: () => Navigator.of(context).pop(),
      composer: StatefulBuilder(
        builder: (context, setState) => Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            TextField(
              key: const ValueKey('new-org-key'),
              controller: _key,
              decoration: const InputDecoration(
                labelText: 'Short id (letters, no spaces)',
              ),
              onChanged: (_) => setState(() {}),
            ),
            const SizedBox(height: Space.md),
            TextField(
              key: const ValueKey('new-org-name'),
              controller: _name,
              decoration: const InputDecoration(labelText: 'Organization name'),
              onChanged: (_) => setState(() {}),
            ),
            const SizedBox(height: Space.md),
            TextField(
              key: const ValueKey('new-org-chief-name'),
              controller: _chiefName,
              decoration: const InputDecoration(labelText: 'Chief name'),
              onChanged: (_) => setState(() {}),
            ),
            const SizedBox(height: Space.md),
            TextField(
              key: const ValueKey('new-org-chief-purpose'),
              controller: _chiefPurpose,
              decoration: const InputDecoration(
                labelText: 'What this chief coordinates',
              ),
            ),
            const SizedBox(height: Space.lg),
            Align(
              alignment: Alignment.centerRight,
              child: FilledButton(
                key: const ValueKey('new-org-create'),
                onPressed: _canSubmit
                    ? () => widget.controller.createOrganization(
                        key: _key.text.trim(),
                        name: _name.text.trim(),
                        chiefKey: '${_key.text.trim()}-chief',
                        chiefName: _chiefName.text.trim(),
                        chiefPurpose: _chiefPurpose.text.trim().isEmpty
                            ? 'Coordinates ${_name.text.trim()}'
                            : _chiefPurpose.text.trim(),
                        chiefInstructions:
                            'Coordinate ${_name.text.trim()} and its workers.',
                      )
                    : null,
                child: const Text('Continue'),
              ),
            ),
          ],
        ),
      ),
    ),
  );
}

class _NewWorkerDialog extends StatefulWidget {
  const _NewWorkerDialog({required this.controller});
  final WorkspaceController controller;

  @override
  State<_NewWorkerDialog> createState() => _NewWorkerDialogState();
}

class _NewWorkerDialogState extends State<_NewWorkerDialog> {
  final _key = TextEditingController();
  final _name = TextEditingController();
  final _purpose = TextEditingController();
  WorkerId? _organization;

  @override
  void dispose() {
    _key.dispose();
    _name.dispose();
    _purpose.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final organizations = [
      for (final w in widget.controller.snapshot.workers)
        if (w.isOrganizationChief) w,
    ];
    _organization ??= organizations.firstOrNull?.id;
    final canSubmit =
        _key.text.trim().isNotEmpty &&
        _name.text.trim().isNotEmpty &&
        _organization != null;
    return _DialogShell(
      title: 'New worker',
      controller: widget.controller,
      listenable: widget.controller,
      builder: (context) => _DraftFlowBody(
        kind: DraftFlowKind.worker,
        controller: widget.controller,
        onDone: () => Navigator.of(context).pop(),
        composer: StatefulBuilder(
          builder: (context, setState) => Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              DropdownButtonFormField<WorkerId>(
                key: const ValueKey('new-worker-organization'),
                value: _organization,
                decoration: const InputDecoration(labelText: 'Organization'),
                items: [
                  for (final o in organizations)
                    DropdownMenuItem(value: o.id, child: Text(o.ancestry)),
                ],
                onChanged: (v) => setState(() => _organization = v),
              ),
              const SizedBox(height: Space.md),
              TextField(
                key: const ValueKey('new-worker-key'),
                controller: _key,
                decoration: const InputDecoration(
                  labelText: 'Short id (letters, no spaces)',
                ),
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: Space.md),
              TextField(
                key: const ValueKey('new-worker-name'),
                controller: _name,
                decoration: const InputDecoration(labelText: 'Worker name'),
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: Space.md),
              TextField(
                key: const ValueKey('new-worker-purpose'),
                controller: _purpose,
                decoration: const InputDecoration(labelText: 'Purpose'),
              ),
              const SizedBox(height: Space.lg),
              Align(
                alignment: Alignment.centerRight,
                child: FilledButton(
                  key: const ValueKey('new-worker-create'),
                  onPressed: canSubmit
                      ? () => widget.controller.createWorker(
                          organizationId: widget.controller.snapshot
                              .worker(_organization!)!
                              .organizationId
                              .value,
                          key: _key.text.trim(),
                          name: _name.text.trim(),
                          purpose: _purpose.text.trim().isEmpty
                              ? 'Works within ${widget.controller.snapshot.worker(_organization!)?.name ?? 'the organization'}'
                              : _purpose.text.trim(),
                          instructions:
                              'Work on what ${_name.text.trim()} is asked, '
                              'within its organization’s scope.',
                        )
                      : null,
                  child: const Text('Continue'),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _NewResponsibilityDialog extends StatefulWidget {
  const _NewResponsibilityDialog({
    required this.controller,
    required this.workerId,
  });
  final WorkspaceController controller;
  final WorkerId workerId;

  @override
  State<_NewResponsibilityDialog> createState() =>
      _NewResponsibilityDialogState();
}

class _NewResponsibilityDialogState extends State<_NewResponsibilityDialog> {
  final _outcome = TextEditingController();
  final _trigger = TextEditingController();
  int _intervalMinutes = 60;
  VerifierIdentity? _verifier;

  @override
  void initState() {
    super.initState();
    widget.controller.loadTrustedVerifiers().then((v) {
      if (mounted) setState(() => _verifier = v.firstOrNull);
    });
  }

  @override
  void dispose() {
    _outcome.dispose();
    _trigger.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final canSubmit = _outcome.text.trim().isNotEmpty && _verifier != null;
    return _DialogShell(
      title: 'New responsibility',
      controller: widget.controller,
      listenable: widget.controller,
      builder: (context) => _DraftFlowBody(
        kind: DraftFlowKind.responsibility,
        controller: widget.controller,
        onDone: () => Navigator.of(context).pop(),
        composer: StatefulBuilder(
          builder: (context, setState) => Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              TextField(
                key: const ValueKey('new-responsibility-outcome'),
                controller: _outcome,
                decoration: const InputDecoration(
                  labelText: 'What this keeps working on',
                ),
                onChanged: (_) => setState(() {}),
              ),
              const SizedBox(height: Space.md),
              TextField(
                key: const ValueKey('new-responsibility-trigger'),
                controller: _trigger,
                decoration: const InputDecoration(
                  labelText: 'What starts a cycle (a trigger, in words)',
                ),
              ),
              const SizedBox(height: Space.md),
              Row(
                children: [
                  Text(
                    'Runs at most every',
                    style: Theme.of(context).textTheme.bodySmall,
                  ),
                  const SizedBox(width: Space.sm),
                  DropdownButton<int>(
                    key: const ValueKey('new-responsibility-interval'),
                    value: _intervalMinutes,
                    items: const [
                      DropdownMenuItem(value: 60, child: Text('hour')),
                      DropdownMenuItem(value: 1440, child: Text('day')),
                      DropdownMenuItem(value: 10080, child: Text('week')),
                    ],
                    onChanged: (v) =>
                        setState(() => _intervalMinutes = v ?? 60),
                  ),
                ],
              ),
              if (_verifier == null)
                const Padding(
                  padding: EdgeInsets.only(top: Space.md),
                  child: Notice('Checking this installation’s verifiers…'),
                ),
              const SizedBox(height: Space.lg),
              Align(
                alignment: Alignment.centerRight,
                child: FilledButton(
                  key: const ValueKey('new-responsibility-create'),
                  onPressed: canSubmit
                      ? () => widget.controller.createResponsibility(
                          workerId: widget.workerId.value,
                          outcome: _outcome.text.trim(),
                          triggers: _trigger.text.trim().isEmpty
                              ? const []
                              : [_trigger.text.trim()],
                          minIntervalSeconds: _intervalMinutes * 60,
                          verifier: _verifier!,
                        )
                      : null,
                  child: const Text('Continue'),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _NewGroupDialog extends StatefulWidget {
  const _NewGroupDialog({required this.controller});
  final WorkspaceController controller;

  @override
  State<_NewGroupDialog> createState() => _NewGroupDialogState();
}

class _NewGroupDialogState extends State<_NewGroupDialog> {
  final _title = TextEditingController();
  final Set<WorkerId> _selected = {};

  @override
  void dispose() {
    _title.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    return ListenableBuilder(
      listenable: widget.controller,
      builder: (context, _) {
        final busy = widget.controller.groupFlowBusy;
        final unknown = widget.controller.groupFlowUnknown;
        final note = widget.controller.groupFlowNote;
        return Dialog(
          insetPadding: const EdgeInsets.all(Space.xl),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 480),
            child: Padding(
              padding: const EdgeInsets.all(28),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Semantics(
                          header: true,
                          namesRoute: true,
                          child: Text('New group', style: text.titleLarge),
                        ),
                      ),
                      IconButton(
                        tooltip: 'Close',
                        onPressed: () => Navigator.of(context).pop(),
                        icon: const Icon(Icons.close),
                      ),
                    ],
                  ),
                  const SizedBox(height: Space.md),
                  const Text(
                    'A group chat carries no organization, grant or memory '
                    'permission.',
                  ),
                  const SizedBox(height: Space.lg),
                  TextField(
                    key: const ValueKey('new-group-title'),
                    controller: _title,
                    decoration: const InputDecoration(labelText: 'Group name'),
                    onChanged: (_) => setState(() {}),
                  ),
                  const SizedBox(height: Space.md),
                  ConstrainedBox(
                    constraints: const BoxConstraints(maxHeight: 220),
                    child: ListView(
                      shrinkWrap: true,
                      children: [
                        for (final w in widget.controller.snapshot.workers)
                          CheckboxListTile(
                            key: ValueKey('new-group-worker-${w.id.value}'),
                            dense: true,
                            value: _selected.contains(w.id),
                            title: Text(w.name),
                            onChanged: (v) => setState(() {
                              if (v ?? false) {
                                _selected.add(w.id);
                              } else {
                                _selected.remove(w.id);
                              }
                            }),
                          ),
                      ],
                    ),
                  ),
                  if (unknown) ...[
                    const SizedBox(height: Space.md),
                    const Notice('Checking whether this was received.'),
                    OutlinedButton(
                      onPressed: widget.controller.checkGroupFlow,
                      child: const Text('Check again'),
                    ),
                  ] else if (note != null) ...[
                    const SizedBox(height: Space.md),
                    Notice(note, icon: Icons.error_outline),
                  ],
                  const SizedBox(height: Space.lg),
                  Align(
                    alignment: Alignment.centerRight,
                    child: FilledButton(
                      key: const ValueKey('new-group-create'),
                      onPressed:
                          busy ||
                              _title.text.trim().isEmpty ||
                              _selected.isEmpty
                          ? null
                          : () async {
                              await widget.controller.createGroup(
                                title: _title.text.trim(),
                                workerParticipantIds: [
                                  for (final id in _selected) id.value,
                                ],
                              );
                              if (context.mounted &&
                                  !widget.controller.groupFlowBusy &&
                                  widget.controller.groupFlowNote == null &&
                                  !widget.controller.groupFlowUnknown) {
                                Navigator.of(context).pop();
                              }
                            },
                      child: Text(busy ? 'Creating…' : 'Create group'),
                    ),
                  ),
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}
