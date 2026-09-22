// New task/delegate task, and a task's own real result artifacts with the
// eligible human's exact review and decision (`task.accept`).

import 'package:flutter/material.dart';

import '../state/snapshot.dart';
import '../state/view_state.dart';
import '../state/workspace_controller.dart';
import 'theme.dart';
import 'widgets.dart';

Future<void> showNewTaskDialog(
  BuildContext context,
  WorkspaceController controller,
  WorkerId workerId,
) => showDialog<void>(
  context: context,
  barrierLabel: 'Close new task',
  builder: (_) => _TaskComposeDialog(
    title: 'New task',
    controller: controller,
    onSubmit: (outcome, outputs, verifier) => controller.createTask(
      workerId: workerId.value,
      outcome: outcome,
      requiredOutputs: outputs,
      verifier: verifier,
    ),
  ),
);

Future<void> showDelegateTaskDialog(
  BuildContext context,
  WorkspaceController controller,
  TaskEntry parent,
  WorkerId childWorkerId,
) => showDialog<void>(
  context: context,
  barrierLabel: 'Close delegate task',
  builder: (_) => _TaskComposeDialog(
    title:
        'Delegate to ${controller.snapshot.worker(childWorkerId)?.name ?? 'worker'}',
    controller: controller,
    onSubmit: (outcome, outputs, verifier) => controller.delegateTask(
      parent: parent,
      childWorkerId: childWorkerId.value,
      outcome: outcome,
      requiredOutputs: outputs,
      verifier: verifier,
    ),
  ),
);

class _TaskComposeDialog extends StatefulWidget {
  const _TaskComposeDialog({
    required this.title,
    required this.controller,
    required this.onSubmit,
  });

  final String title;
  final WorkspaceController controller;
  final void Function(
    String outcome,
    List<String> requiredOutputs,
    VerifierIdentity verifier,
  )
  onSubmit;

  @override
  State<_TaskComposeDialog> createState() => _TaskComposeDialogState();
}

class _TaskComposeDialogState extends State<_TaskComposeDialog> {
  final _outcome = TextEditingController();
  final _output = TextEditingController();
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
    _output.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    return ListenableBuilder(
      listenable: widget.controller,
      builder: (context, _) {
        final flow = widget.controller.taskFlow;
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
                          child: Text(widget.title, style: text.titleLarge),
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
                    'Decided by you: an eligible human reviews the real '
                    'result and accepts or rejects it, not an automated '
                    'check.',
                  ),
                  const SizedBox(height: Space.lg),
                  if (flow.phase == TaskFlowPhase.composing) ...[
                    TextField(
                      key: const ValueKey('new-task-outcome'),
                      controller: _outcome,
                      decoration: const InputDecoration(
                        labelText: 'What should be produced',
                      ),
                      onChanged: (_) => setState(() {}),
                    ),
                    const SizedBox(height: Space.md),
                    TextField(
                      key: const ValueKey('new-task-output'),
                      controller: _output,
                      decoration: const InputDecoration(
                        labelText: 'Required output name',
                        hintText: 'for example: report.md',
                      ),
                      onChanged: (_) => setState(() {}),
                    ),
                    if (_verifier == null)
                      const Padding(
                        padding: EdgeInsets.only(top: Space.md),
                        child: Notice(
                          'Checking this installation’s verifiers…',
                        ),
                      ),
                    const SizedBox(height: Space.lg),
                    Align(
                      alignment: Alignment.centerRight,
                      child: FilledButton(
                        key: const ValueKey('new-task-create'),
                        onPressed:
                            _outcome.text.trim().isNotEmpty &&
                                _output.text.trim().isNotEmpty &&
                                _verifier != null
                            ? () => widget.onSubmit(_outcome.text.trim(), [
                                _output.text.trim(),
                              ], _verifier!)
                            : null,
                        child: const Text('Create and start'),
                      ),
                    ),
                  ] else if (flow.isBusy)
                    const Padding(
                      padding: EdgeInsets.symmetric(vertical: Space.xl),
                      child: Center(child: CircularProgressIndicator()),
                    )
                  else if (flow.phase == TaskFlowPhase.creatingUnknown ||
                      flow.phase == TaskFlowPhase.startingUnknown) ...[
                    const Notice(
                      'Checking whether this was received. Nothing is '
                      'resent.',
                    ),
                    const SizedBox(height: Space.md),
                    OutlinedButton(
                      key: const ValueKey('task-flow-check'),
                      onPressed: widget.controller.checkTaskFlow,
                      child: const Text('Check again'),
                    ),
                  ] else if (flow.phase == TaskFlowPhase.started) ...[
                    const Notice(
                      'Started. It will show in Work as it runs.',
                      icon: Icons.check_circle_outline,
                    ),
                    const SizedBox(height: Space.lg),
                    Align(
                      alignment: Alignment.centerRight,
                      child: FilledButton(
                        key: const ValueKey('task-flow-done'),
                        onPressed: () {
                          widget.controller.cancelTaskFlow();
                          Navigator.of(context).pop();
                        },
                        child: const Text('Done'),
                      ),
                    ),
                  ] else ...[
                    Notice(flow.note ?? 'This was refused.', icon: Icons.block),
                    const SizedBox(height: Space.lg),
                    Wrap(
                      spacing: Space.sm,
                      children: [
                        OutlinedButton(
                          onPressed: () {
                            widget.controller.cancelTaskFlow();
                            Navigator.of(context).pop();
                          },
                          child: const Text('Close'),
                        ),
                      ],
                    ),
                  ],
                ],
              ),
            ),
          ),
        );
      },
    );
  }
}

Future<void> showTaskResultsDialog(
  BuildContext context,
  WorkspaceController controller,
  TaskEntry task,
) => showDialog<void>(
  context: context,
  barrierLabel: 'Close task results',
  builder: (_) => _TaskResultsDialog(controller: controller, task: task),
);

class _TaskResultsDialog extends StatefulWidget {
  const _TaskResultsDialog({required this.controller, required this.task});
  final WorkspaceController controller;
  final TaskEntry task;

  @override
  State<_TaskResultsDialog> createState() => _TaskResultsDialogState();
}

class _TaskResultsDialogState extends State<_TaskResultsDialog> {
  List<TaskArtifactEntry>? _artifacts;
  final Map<String, ReviewContentPart> _read = {};
  bool _deciding = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final artifacts = await widget.controller.loadTaskArtifacts(widget.task.id);
    if (mounted) setState(() => _artifacts = artifacts);
  }

  Future<void> _readOne(TaskArtifactEntry a) async {
    final part = await widget.controller.readTaskArtifact(a);
    if (mounted) setState(() => _read[a.id] = part);
  }

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    final artifacts = _artifacts;
    final task = widget.task;
    return Dialog(
      insetPadding: const EdgeInsets.all(Space.xl),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 560),
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
                      child: Text(task.title, style: text.titleLarge),
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
              Flexible(
                child: SingleChildScrollView(
                  child: artifacts == null
                      ? const Padding(
                          padding: EdgeInsets.symmetric(vertical: Space.xl),
                          child: Center(child: CircularProgressIndicator()),
                        )
                      : artifacts.isEmpty
                      ? const EmptyState('No result artifacts yet.')
                      : Column(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            for (final a in artifacts)
                              Padding(
                                padding: const EdgeInsets.only(
                                  bottom: Space.md,
                                ),
                                child: Container(
                                  padding: const EdgeInsets.all(Space.lg),
                                  decoration: BoxDecoration(
                                    border: Border.all(
                                      color: ZatitiPalette.of(context).line,
                                    ),
                                    borderRadius: BorderRadius.circular(
                                      Measure.cardRadius,
                                    ),
                                  ),
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.start,
                                    children: [
                                      Text(
                                        '${a.mediaType} · ${a.sizeBytes} bytes',
                                        style: text.titleSmall,
                                      ),
                                      const SizedBox(height: 4),
                                      Text(
                                        a.createdAt.toIso8601String(),
                                        style: text.bodySmall,
                                      ),
                                      const SizedBox(height: Space.sm),
                                      if (_read[a.id] != null)
                                        Text(
                                          _read[a.id]!.text ??
                                              _read[a.id]!.unavailableReason ??
                                              '',
                                          style: text.bodySmall,
                                        )
                                      else
                                        OutlinedButton(
                                          onPressed: () => _readOne(a),
                                          child: const Text(
                                            'Read exact content',
                                          ),
                                        ),
                                    ],
                                  ),
                                ),
                              ),
                          ],
                        ),
                ),
              ),
              if (task.manualAcceptance) ...[
                const SizedBox(height: Space.lg),
                const Notice(
                  'This task is decided by you, not an automated check.',
                ),
                const SizedBox(height: Space.md),
                Wrap(
                  spacing: Space.sm,
                  children: [
                    OutlinedButton(
                      key: const ValueKey('task-results-reject'),
                      onPressed: _deciding
                          ? null
                          : () async {
                              setState(() => _deciding = true);
                              await widget.controller.decideTask(
                                task,
                                accept: false,
                              );
                              if (context.mounted) Navigator.of(context).pop();
                            },
                      child: const Text('Reject'),
                    ),
                    FilledButton(
                      key: const ValueKey('task-results-accept'),
                      onPressed: _deciding
                          ? null
                          : () async {
                              setState(() => _deciding = true);
                              await widget.controller.decideTask(
                                task,
                                accept: true,
                              );
                              if (context.mounted) Navigator.of(context).pop();
                            },
                      child: const Text('Accept'),
                    ),
                  ],
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}
