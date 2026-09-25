// WorkerDetailsPanel: durable work kept close but not constantly visible.
// Five tabs: Work, Routines, Files, Memory, Access. Every entry derives from
// the same snapshot as the sidebar and the conversation.

import 'package:flutter/material.dart';

import '../state/snapshot.dart';
import '../state/view_state.dart';
import '../state/workspace_controller.dart';
import 'action_review_dialog.dart';
import 'creation_dialogs.dart';
import 'memory_dialogs.dart';
import 'task_dialogs.dart';
import 'provider_model_editor.dart';
import 'theme.dart';
import 'widgets.dart';

class WorkerDetailsPanel extends StatelessWidget {
  const WorkerDetailsPanel({
    super.key,
    required this.controller,
    required this.worker,
  });

  final WorkspaceController controller;
  final WorkerEntry worker;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    final tab = controller.detailsTab;
    return Semantics(
      container: true,
      label: '${worker.name}’s details',
      explicitChildNodes: true,
      child: Material(
        color: p.sidebar,
        child: FocusTraversalGroup(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(
                  Space.xl,
                  Space.xl,
                  Space.md,
                  Space.sm,
                ),
                child: Row(
                  children: [
                    Expanded(
                      child: Semantics(
                        header: true,
                        child: Text(
                          '${worker.name}’s details',
                          style: text.titleMedium,
                        ),
                      ),
                    ),
                    IconButton(
                      key: ValueKey('worker-model-${worker.id.value}'),
                      tooltip: 'Choose model profile',
                      onPressed: () => showProviderModelEditor(
                        context,
                        controller: controller,
                        workerId: worker.id.value,
                      ),
                      icon: const Icon(Icons.tune),
                    ),
                    IconButton(
                      key: const ValueKey('details-close'),
                      tooltip: 'Close details',
                      onPressed: controller.closeDetails,
                      icon: const Icon(Icons.close),
                    ),
                  ],
                ),
              ),
              // Tabs wrap instead of scrolling, so enlarged text never hides one.
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: Space.md),
                child: Wrap(
                  children: [
                    for (final t in DetailsTab.values)
                      _TabButton(
                        tab: t,
                        selected: t == tab,
                        onTap: () => controller.setDetailsTab(t),
                      ),
                  ],
                ),
              ),
              Divider(color: p.line),
              Expanded(
                child: ListView(
                  key: PageStorageKey('details-${worker.id.value}-${tab.name}'),
                  padding: const EdgeInsets.all(Space.xl),
                  children: [
                    if (controller.showsSavedView)
                      const Padding(
                        padding: EdgeInsets.only(bottom: Space.lg),
                        child: Notice('Saved view · may be out of date'),
                      ),
                    for (final n in controller.prerequisitesFor(tab))
                      _Prerequisite(n),
                    ..._content(context, tab),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  List<Widget> _content(BuildContext context, DetailsTab tab) {
    final text = Theme.of(context).textTheme;
    Widget note(String s) => Padding(
      padding: const EdgeInsets.only(bottom: Space.lg),
      child: Text(s, style: text.bodySmall!.copyWith(fontSize: 13)),
    );
    final hasNotice = controller.prerequisitesFor(tab).isNotEmpty;

    switch (tab) {
      case DetailsTab.work:
        final decisions = [
          for (final d in controller.decisionsFor(worker.id))
            if (d.phase.needsYou) d,
        ];
        final tasks = controller.tasksFor(worker.id);
        List<TaskEntry> inState(Set<TaskEntryState> s) => [
          for (final t in tasks)
            if (s.contains(t.state)) t,
        ];
        final active = inState({
          TaskEntryState.inProgress,
          TaskEntryState.waiting,
        });
        final changes = inState({TaskEntryState.needsChanges});
        final done = inState({TaskEntryState.completed});
        final setup = controller.prerequisitesForWorker(worker.id);
        final other = controller.unresolvedOperationsFor(worker.id);
        return [
          note(
            worker.parentId == null
                ? 'A shared view of the work ${worker.name} coordinates.'
                : 'Work assigned to ${worker.name}.',
          ),
          Padding(
            padding: const EdgeInsets.only(bottom: Space.lg),
            child: Wrap(
              spacing: Space.sm,
              children: [
                OutlinedButton(
                  key: ValueKey('new-task-${worker.id.value}'),
                  onPressed: () =>
                      showNewTaskDialog(context, controller, worker.id),
                  child: const Text('New task'),
                ),
                OutlinedButton(
                  key: ValueKey('new-responsibility-${worker.id.value}'),
                  onPressed: () => showNewResponsibilityDialog(
                    context,
                    controller,
                    worker.id,
                  ),
                  child: const Text('New responsibility'),
                ),
              ],
            ),
          ),
          if (setup.isNotEmpty) const SectionLabel('Setup needed'),
          for (final s in setup)
            MiniCard(
              title: s.title,
              status: 'Needs setup',
              statusIcon: Icons.info_outline,
              statusIsDecision: true,
              body: s.message,
            ),
          if (decisions.isNotEmpty) const SectionLabel('Needs your decision'),
          for (final d in decisions)
            MiniCard(
              title: d.review.title,
              status: d.phase.label,
              statusIcon: Icons.shield_outlined,
              statusIsDecision: true,
              body: '${d.proposerName} · ${d.review.destination}',
              actions: [
                OutlinedButton(
                  onPressed: () =>
                      showActionReviewDialog(context, controller, d.review.id),
                  child: const Text('Review request'),
                ),
              ],
            ),
          if (changes.isNotEmpty) const SectionLabel('Needs changes'),
          for (final t in changes)
            MiniCard(
              title: t.title,
              status: 'Needs changes',
              statusIcon: Icons.error_outline,
              statusIsDecision: true,
              body: t.detail,
              footnotes: [_owner(t.workerId)],
              actions: [
                if (t.manualAcceptance)
                  OutlinedButton(
                    key: ValueKey('task-results-${t.id}'),
                    onPressed: () =>
                        showTaskResultsDialog(context, controller, t),
                    child: const Text('Review & decide'),
                  ),
                OutlinedButton(
                  onPressed: () =>
                      showDelegateTaskDialog(context, controller, t, worker.id),
                  child: const Text('Delegate'),
                ),
              ],
            ),
          if (active.isNotEmpty) const SectionLabel('In progress'),
          for (final t in active)
            MiniCard(
              title: t.title,
              status: t.state == TaskEntryState.waiting
                  ? 'Waiting'
                  : 'In progress',
              statusIcon: Icons.schedule,
              body: t.detail,
              footnotes: [_owner(t.workerId)],
              actions: [
                if (t.manualAcceptance)
                  OutlinedButton(
                    key: ValueKey('task-results-${t.id}'),
                    onPressed: () =>
                        showTaskResultsDialog(context, controller, t),
                    child: const Text('Review & decide'),
                  ),
                OutlinedButton(
                  onPressed: () =>
                      showDelegateTaskDialog(context, controller, t, worker.id),
                  child: const Text('Delegate'),
                ),
              ],
            ),
          if (done.isNotEmpty) const SectionLabel('Completed'),
          for (final t in done)
            MiniCard(
              title: t.title,
              status: t.detail.isEmpty ? 'Completed' : t.detail,
              statusIcon: Icons.check,
              footnotes: [_owner(t.workerId)],
              actions: [
                OutlinedButton(
                  onPressed: () =>
                      showTaskResultsDialog(context, controller, t),
                  child: const Text('View results'),
                ),
              ],
            ),
          if (other.isNotEmpty) const SectionLabel('Other activity'),
          for (final o in other)
            MiniCard(
              key: ValueKey('unresolved-op-${o.id}'),
              title: o.destination,
              status: o.label,
              statusIcon: Icons.hourglass_top_outlined,
              footnotes: [_owner(worker.id)],
            ),
          if (decisions.isEmpty && tasks.isEmpty && other.isEmpty && !hasNotice)
            const EmptyState('No work yet. Start in the conversation.'),
        ];

      case DetailsTab.routines:
        final routines = controller.routinesFor(worker.id);
        return [
          note(
            'Ongoing responsibilities keep working without a new message '
            'each time.',
          ),
          for (final r in routines) _routine(r),
          if (routines.isEmpty && !hasNotice)
            const EmptyState(
              'No ongoing responsibilities in this conversation.',
            ),
        ];

      case DetailsTab.files:
        final files = controller.filesFor(worker.id);
        return [
          note('Results stay here, even after the conversation moves on.'),
          for (final f in files) _file(f),
          if (files.isEmpty && !hasNotice)
            const EmptyState('No completed files in this conversation.'),
        ];

      case DetailsTab.memory:
        final memory = controller.memoryFor(worker.id);
        return [
          note(
            'Read-only claims with source, freshness and retraction. New '
            'memory is added through conversation, not this panel.',
          ),
          for (final m in memory) _memory(context, m),
          if (memory.isEmpty && !hasNotice)
            const EmptyState('No shared preferences in this scope.'),
        ];

      case DetailsTab.access:
        final access = controller.accessFor(worker.id);
        final spending = controller.spendingFor(worker.id);
        final autonomy = controller.autonomyFor(worker.id);
        final recovery = controller.recoveryFor(worker.id);
        return [
          note(
            '${worker.name}’s permissions are specific. New '
            'responsibilities do not add permissions.',
          ),
          for (final a in access)
            MiniCard(
              title: a.title,
              status: a.allowed ? 'Allowed' : 'Decision required',
              statusIcon: a.allowed ? Icons.check : Icons.shield_outlined,
              statusIsDecision: !a.allowed,
              body: a.detail,
              actions: [
                if (a.reviewId != null)
                  OutlinedButton(
                    onPressed: () => showActionReviewDialog(
                      context,
                      controller,
                      a.reviewId!,
                    ),
                    child: const Text('Inspect exact request'),
                  ),
              ],
            ),
          if (autonomy.isNotEmpty)
            const SectionLabel('Autonomy — exact capability, not a score'),
          for (final a in autonomy) _autonomy(a),
          if (spending != null) ...[
            const SectionLabel('Cost and execution posture'),
            _Spending(spending),
          ],
          if (recovery.isNotEmpty) const SectionLabel('Recovery obligations'),
          for (final r in recovery) _recovery(r),
          if (access.isEmpty &&
              spending == null &&
              autonomy.isEmpty &&
              recovery.isEmpty &&
              !hasNotice)
            const EmptyState('No access is recorded for this worker.'),
        ];
    }
  }

  Widget _file(FileEntry f) => MiniCard(
    key: ValueKey('file-${f.id}'),
    title: f.title,
    status: f.verified ? 'Available' : 'Integrity failure',
    statusIcon: f.verified ? Icons.description_outlined : Icons.error_outline,
    statusIsDecision: !f.verified,
    body: f.detail,
    footnotes: [
      'Digest: ${f.digest.isEmpty ? 'not recorded' : f.digest}',
      'Sharing: ${f.classification}',
      if (f.taskId != null) 'From task: ${f.taskTitle ?? f.taskId}',
      for (final check in f.checks) 'Sealed check — $check',
      _owner(f.workerId),
    ],
  );

  Widget _memory(BuildContext context, MemoryClaimView v) {
    final claim = v.claim;
    return MiniCard(
      key: ValueKey('memory-${claim.id.value}'),
      title: claim.title,
      status: v.statusLabel,
      statusIcon: claim.active ? Icons.check_circle_outline : Icons.block,
      statusIsDecision: !claim.active,
      body: claim.text,
      footnotes: [
        ...claim.provenance,
        if (claim.confidence != null)
          'Confidence: ${(claim.confidence! / 10000).toStringAsFixed(1)}%',
        if (!claim.canRetract && claim.active)
          'Retraction is not authorized for this claim from this client.',
      ],
      actions: [
        if (v.canRetract)
          OutlinedButton(
            key: ValueKey('memory-retract-${claim.id.value}'),
            onPressed: () =>
                showMemoryRetractDialog(context, controller, claim),
            child: const Text('Retract'),
          ),
      ],
    );
  }

  Widget _autonomy(AutonomyEntry a) => MiniCard(
    key: ValueKey('autonomy-${a.id}'),
    title: a.capability,
    status: switch (a.state) {
      'qualified' => 'Qualified',
      'restricted' => 'Restricted',
      'rejected' => 'Not qualified',
      'proposed' => 'Proposed · awaiting evidence',
      'expired' => 'Expired',
      _ => a.state,
    },
    statusIcon: a.state == 'qualified' ? Icons.check : Icons.shield_outlined,
    statusIsDecision: a.state != 'qualified',
    body: a.explanation,
    footnotes: [
      if (a.destinations.isNotEmpty)
        'Destinations: ${a.destinations.join(', ')}',
    ],
  );

  Widget _recovery(RecoveryEntry r) => MiniCard(
    key: ValueKey('recovery-${r.id}'),
    title: r.label,
    status: 'Needs review',
    statusIcon: Icons.build_circle_outlined,
    statusIsDecision: true,
    footnotes: r.obligations,
  );

  String _owner(WorkerId id) {
    final w = controller.snapshot.worker(id);
    if (w == null) return '';
    return [w.name, ...w.organizationPath.skip(1).take(1)].join(' · ');
  }

  /// A trigger string is never rendered as a schedule: a real cron-like
  /// `Schedule` (when one links this worker) shows its own expression,
  /// timezone and misfire policy; otherwise this names exactly what admits a
  /// cycle, honestly labeled as event-driven.
  String _scheduleLine(RoutineEntry routine) {
    final s = routine.schedule;
    if (s != null) {
      final paused = s.paused ? ' · schedule paused' : '';
      return '${s.expression} (${s.timezone}) · misfire: ${s.misfire}$paused';
    }
    return routine.triggers.isEmpty
        ? 'Event-driven: runs when a watched signal changes. No cron-like '
              'schedule links this worker.'
        : 'Event-driven on: ${routine.triggers.join(', ')}. No cron-like '
              'schedule links this worker.';
  }

  Widget _routine(RoutineView r) => MiniCard(
    key: ValueKey('routine-${r.routine.id.value}'),
    title: r.routine.title,
    status: r.label,
    statusIcon: r.phase == RoutinePhase.paused ? Icons.pause : Icons.schedule,
    body: _scheduleLine(r.routine),
    footnotes: [
      if (r.routine.signals.isNotEmpty)
        'Watches: ${r.routine.signals.join(', ')}',
      'Minimum interval between cycles: ${r.routine.minIntervalSeconds}s',
      r.routine.nextRun != null
          ? 'Next wake: ${r.routine.nextRun!.toIso8601String()}'
          : 'No next-wake decision recorded yet.',
      if (r.routine.schedule?.nextWake != null)
        'Schedule next wake: ${r.routine.schedule!.nextWake!.toIso8601String()}',
      r.routine.lastCycleId != null
          ? 'Last cycle: ${r.routine.lastCycleId}'
          : 'No cycle has run yet.',
      if (r.phase != RoutinePhase.paused)
        'Pausing stops new runs of this responsibility only. Work already '
            'started continues, and unrelated tasks are not cancelled.',
      ?r.note,
    ],
    actions: [
      if (r.phase == RoutinePhase.acknowledgmentUnknown)
        OutlinedButton(
          onPressed: controller.isOnline
              ? () => controller.checkPause(r.routine.id)
              : null,
          child: const Text('Check again'),
        )
      else if (r.phase != RoutinePhase.paused)
        OutlinedButton(
          key: ValueKey('pause-${r.routine.id.value}'),
          onPressed: r.canPause
              ? () => controller.pauseRoutine(r.routine.id)
              : null,
          child: Text(
            r.phase == RoutinePhase.submitting
                ? 'Pausing…'
                : 'Pause responsibility',
          ),
        ),
    ],
  );
}

class _TabButton extends StatelessWidget {
  const _TabButton({
    required this.tab,
    required this.selected,
    required this.onTap,
  });

  final DetailsTab tab;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    return Semantics(
      label: '${tab.label} tab',
      button: true,
      selected: selected,
      excludeSemantics: true,
      onTap: onTap,
      child: InkWell(
        key: ValueKey('details-tab-${tab.name}'),
        onTap: onTap,
        borderRadius: BorderRadius.circular(Measure.controlRadius),
        child: Container(
          constraints: const BoxConstraints(minHeight: 44),
          padding: const EdgeInsets.symmetric(
            horizontal: 7,
            vertical: Space.md,
          ),
          decoration: BoxDecoration(
            border: Border(
              bottom: BorderSide(
                color: selected ? p.accent : Colors.transparent,
                width: 2,
              ),
            ),
          ),
          child: Text(
            tab.label,
            style: Theme.of(context).textTheme.labelLarge!.copyWith(
              color: selected ? p.text : p.muted,
            ),
          ),
        ),
      ),
    );
  }
}

class _Prerequisite extends StatelessWidget {
  const _Prerequisite(this.notice);
  final PrerequisiteNotice notice;

  @override
  Widget build(BuildContext context) => MiniCard(
    title: notice.title,
    status: 'Not available yet',
    statusIcon: Icons.info_outline,
    statusIsDecision: true,
    body: notice.message,
    footnotes: [if (notice.setupPath != null) 'Set up: ${notice.setupPath}'],
  );
}

class _Spending extends StatelessWidget {
  const _Spending(this.entry);
  final SpendingEntry entry;

  @override
  Widget build(BuildContext context) {
    final p = ZatitiPalette.of(context);
    final text = Theme.of(context).textTheme;
    return Container(
      padding: const EdgeInsets.all(Space.lg),
      decoration: BoxDecoration(
        color: p.card,
        border: Border.all(color: p.line),
        borderRadius: BorderRadius.circular(Measure.cardRadius),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(entry.headline, style: text.titleSmall),
          if (entry.fraction != null)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: Space.md),
              child: ClipRRect(
                borderRadius: BorderRadius.circular(3),
                child: LinearProgressIndicator(
                  value: entry.fraction!.clamp(0, 1).toDouble(),
                  minHeight: 5,
                  backgroundColor: p.hover,
                  color: p.accent,
                  semanticsLabel: 'Share of the limit spent',
                ),
              ),
            ),
          Text(entry.detail, style: text.bodySmall),
          if (entry.ceiling != null)
            Padding(
              padding: const EdgeInsets.only(top: Space.sm),
              child: Text(entry.ceiling!, style: text.bodySmall),
            ),
          if (entry.contextCapture != null)
            Padding(
              padding: const EdgeInsets.only(top: Space.sm),
              child: Text(switch (entry.contextCapture) {
                'complete' => 'Context capture: complete.',
                'partial' =>
                  'Context capture: partial — some execution '
                      'context may not be captured.',
                'advisory' =>
                  'Context capture: advisory only — outputs '
                      'here are suggestions, never confirmed state.',
                final other => 'Context capture: $other.',
              }, style: text.bodySmall),
            ),
        ],
      ),
    );
  }
}
