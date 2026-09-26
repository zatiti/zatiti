// A projection of authoritative controller records for installed Mac startup.
// A socket or an arbitrary first chat never establishes first-chat readiness.

import '../api/models.dart' as wire;

class InstalledChief {
  const InstalledChief({
    this.workerId,
    this.conversationId,
    required this.issue,
  });
  final String? workerId;
  final String? conversationId;
  final String? issue;
  bool get ready => workerId != null && conversationId != null && issue == null;
}

InstalledChief installedChiefFromRecords({
  required String installationId,
  required bool initialized,
  required bool? runtimeReady,
  required bool paused,
  required bool maintenance,
  required List<wire.Requirement> requirements,
  required List<wire.Organization> organizations,
  required List<wire.Worker> workers,
  required List<wire.Conversation> conversations,
  required List<wire.Connection> connections,
  wire.Limits? effectiveBudget,
  DateTime? now,
}) {
  final roots = organizations.where((o) => o.parentId == null).toList();
  if (roots.length != 1) {
    return const InstalledChief(
      issue: 'The personal organization could not be identified.',
    );
  }
  final root = roots.single;
  final chiefs = workers
      .where((w) => w.id == root.chiefId && w.organizationId == root.id)
      .toList();
  if (chiefs.length != 1) {
    return const InstalledChief(
      issue: 'The personal chief could not be identified.',
    );
  }
  final chief = chiefs.single;
  final pinned = conversations
      .where(
        (c) =>
            c.pinned &&
            c.kind == wire.ConversationKind.direct &&
            c.scope.installationId == installationId &&
            c.participantIds.length == 2 &&
            c.participantIds.toSet().length == 2 &&
            c.participantIds.contains(chief.id) &&
            !c.participantIds.any(
              (p) => p != chief.id && workers.any((w) => w.id == p),
            ),
      )
      .toList();
  if (pinned.length != 1) {
    return InstalledChief(
      workerId: chief.id,
      issue: 'The pinned personal-chief conversation is missing or ambiguous.',
    );
  }
  final conversation = pinned.single;
  InstalledChief blocked(String message) => InstalledChief(
    workerId: chief.id,
    conversationId: conversation.id,
    issue: message,
  );
  // The public conversation record does not identify the bootstrap owner;
  // exactly two distinct participants including chief_id is the strongest
  // available identity evidence without guessing from names or list order.
  if (!initialized || runtimeReady != true || paused || maintenance) {
    return blocked('The installation is not ready for a conversation.');
  }
  if (requirements.isNotEmpty) {
    return blocked(
      'The controller reports remaining setup requirements. Review the prerequisites panel.',
    );
  }
  final profile = chief.profile;
  if (profile == null ||
      profile.executor != 'hosted' ||
      profile.model.isEmpty) {
    return blocked(
      'The personal chief needs a committed hosted model profile.',
    );
  }
  final linked = connections
      .where((c) => c.id == profile.connectionId)
      .toList();
  if (linked.length != 1 ||
      linked.single.validationState != wire.ConnectionValidationState.valid) {
    return blocked('The personal chief needs a validated provider connection.');
  }
  final limits = chief.limits;
  final budget = effectiveBudget;
  final instant = now ?? DateTime.now().toUtc();
  bool usable(wire.Limits? x) =>
      x != null &&
      !x.isUnconfigured &&
      x.spendMicroUnits > 0 &&
      x.modelSteps > 0 &&
      x.concurrency > 0 &&
      x.rootDeadline.isAfter(instant);
  if (!usable(limits) ||
      !usable(budget) ||
      budget!.currency != limits!.currency) {
    return blocked('The personal chief needs committed spend and step limits.');
  }
  return InstalledChief(
    workerId: chief.id,
    conversationId: conversation.id,
    issue: null,
  );
}
