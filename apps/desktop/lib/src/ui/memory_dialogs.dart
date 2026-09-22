// A memory claim's retraction: the one authoring action the Memory tab
// offers, bound to the exact claim version the person saw and only ever
// shown once `MemoryEntry.canRetract` is true.

import 'package:flutter/material.dart';

import '../state/snapshot.dart';
import '../state/view_state.dart';
import '../state/workspace_controller.dart';
import 'theme.dart';
import 'widgets.dart';

Future<void> showMemoryRetractDialog(
  BuildContext context,
  WorkspaceController controller,
  MemoryEntry claim,
) => showDialog<void>(
  context: context,
  barrierLabel: 'Close retract memory',
  builder: (_) => _MemoryRetractDialog(controller: controller, claim: claim),
);

class _MemoryRetractDialog extends StatefulWidget {
  const _MemoryRetractDialog({required this.controller, required this.claim});
  final WorkspaceController controller;
  final MemoryEntry claim;

  @override
  State<_MemoryRetractDialog> createState() => _MemoryRetractDialogState();
}

class _MemoryRetractDialogState extends State<_MemoryRetractDialog> {
  final _reason = TextEditingController();
  bool _submitting = false;

  @override
  void dispose() {
    _reason.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final text = Theme.of(context).textTheme;
    return ListenableBuilder(
      listenable: widget.controller,
      builder: (context, _) {
        final view = widget.controller
            .memoryFor(widget.claim.workerId)
            .where((v) => v.claim.id == widget.claim.id)
            .toList();
        final current = view.isEmpty ? null : view.first;
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
                          child: Text('Retract memory', style: text.titleLarge),
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
                  Text(widget.claim.text, style: text.bodyMedium),
                  const SizedBox(height: Space.md),
                  const Text(
                    'This removes the claim from future recall immediately. '
                    'It cannot erase context already disclosed in a prior '
                    'run, backup or Git history, and the claim stays listed '
                    'here as retracted, never hidden.',
                  ),
                  const SizedBox(height: Space.lg),
                  if (current == null || current.phase == null) ...[
                    TextField(
                      key: const ValueKey('memory-retract-reason'),
                      controller: _reason,
                      decoration: const InputDecoration(
                        labelText: 'Why this is being retracted',
                      ),
                      onChanged: (_) => setState(() {}),
                    ),
                    const SizedBox(height: Space.lg),
                    Align(
                      alignment: Alignment.centerRight,
                      child: FilledButton(
                        key: const ValueKey('memory-retract-submit'),
                        onPressed:
                            !_submitting && _reason.text.trim().isNotEmpty
                            ? () async {
                                setState(() => _submitting = true);
                                await widget.controller.retractClaim(
                                  widget.claim.id,
                                  _reason.text.trim(),
                                );
                                if (mounted) {
                                  setState(() => _submitting = false);
                                }
                              }
                            : null,
                        child: Text(_submitting ? 'Retracting…' : 'Retract'),
                      ),
                    ),
                  ] else if (current.phase == ClaimActionPhase.submitting) ...[
                    const Padding(
                      padding: EdgeInsets.symmetric(vertical: Space.xl),
                      child: Center(child: CircularProgressIndicator()),
                    ),
                  ] else if (current.phase ==
                      ClaimActionPhase.acknowledgmentUnknown) ...[
                    const Notice(
                      'Checking whether this was received. Nothing is '
                      'resent.',
                    ),
                    const SizedBox(height: Space.md),
                    OutlinedButton(
                      key: const ValueKey('memory-retract-check'),
                      onPressed: () =>
                          widget.controller.checkMemoryRetract(widget.claim.id),
                      child: const Text('Check again'),
                    ),
                  ] else ...[
                    Notice(
                      current.note ?? 'Retraction requested.',
                      icon: Icons.check_circle_outline,
                    ),
                    const SizedBox(height: Space.md),
                    Wrap(
                      spacing: Space.sm,
                      children: [
                        OutlinedButton(
                          key: const ValueKey('memory-retract-status'),
                          onPressed: () => widget.controller
                              .checkMemoryJobStatus(widget.claim.id),
                          child: const Text('Check status'),
                        ),
                        OutlinedButton(
                          onPressed: () => Navigator.of(context).pop(),
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
