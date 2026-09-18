// WorkerDetailsPanel: durable work kept close but not constantly visible.
// Five tabs: Work, Routines, Files, Memory, Access. Every entry derives from
// the same snapshot as the sidebar and the conversation.

import 'package:flutter/material.dart';

import '../state/snapshot.dart';
import '../state/view_state.dart';
import '../state/workspace_controller.dart';
import 'action_review_dialog.dart';
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
        return [
          note(
            worker.parentId == null
                ? 'A shared view of the work ${worker.name} coordinates.'
                : 'Work assigned to ${worker.name}.',
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
            ),
          if (done.isNotEmpty) const SectionLabel('Completed'),
          for (final t in done)
            MiniCard(
              title: t.title,
              status: t.detail.isEmpty ? 'Completed' : t.detail,
              statusIcon: Icons.check,
              footnotes: [_owner(t.workerId)],
            ),
          if (decisions.isEmpty && tasks.isEmpty && !hasNotice)
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
          for (final f in files)
            MiniCard(
              title: f.title,
              status: f.verified ? 'Available' : 'Not verified',
              statusIcon: Icons.description_outlined,
              body: f.detail,
              footnotes: [_owner(f.workerId)],
            ),
          if (files.isEmpty && !hasNotice)
            const EmptyState('No completed files in this conversation.'),
        ];

      case DetailsTab.memory:
        final memory = controller.memoryFor(worker.id);
        return [
          note('Useful context, with a clear source and sharing scope.'),
          for (final m in memory)
            MiniCard(
              title: m.title,
              status: 'Shared preference',
              body: m.text,
              footnotes: m.provenance,
            ),
          if (memory.isEmpty && !hasNotice)
            const EmptyState('No shared preferences in this scope.'),
        ];

      case DetailsTab.access:
        final access = controller.accessFor(worker.id);
        final spending = controller.spendingFor(worker.id);
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
          if (spending != null) ...[
            const SectionLabel('Spending'),
            _Spending(spending),
          ],
          if (access.isEmpty && spending == null && !hasNotice)
            const EmptyState('No access is recorded for this worker.'),
        ];
    }
  }

  String _owner(WorkerId id) {
    final w = controller.snapshot.worker(id);
    if (w == null) return '';
    return [w.name, ...w.organizationPath.skip(1).take(1)].join(' · ');
  }

  Widget _routine(RoutineView r) => MiniCard(
    title: r.routine.title,
    status: r.label,
    statusIcon: r.phase == RoutinePhase.paused ? Icons.pause : Icons.schedule,
    body: r.routine.schedule,
    footnotes: [
      if (r.routine.boundary.isNotEmpty) r.routine.boundary,
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
        ],
      ),
    );
  }
}
