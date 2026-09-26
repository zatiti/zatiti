// ignore_for_file: prefer_interpolation_to_compose_strings

import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import '../api/models.dart' as wire;
import '../app/credential_store.dart';
import '../state/live_source.dart';
import '../state/workspace_controller.dart';
import '../state/workspace_source.dart';

Future<bool?> showProviderProfileSetup(
  BuildContext context, {
  required WorkspaceController controller,
  required CredentialStore? credentials,
}) => showDialog<bool>(
  context: context,
  barrierLabel: 'Close model setup',
  builder: (_) => _ModelSetup(controller: controller, credentials: credentials),
);

class _ModelSetup extends StatefulWidget {
  const _ModelSetup({required this.controller, required this.credentials});
  final WorkspaceController controller;
  final CredentialStore? credentials;
  @override
  State<_ModelSetup> createState() => _ModelSetupState();
}

class _ModelSetupState extends State<_ModelSetup> {
  LiveWorkspaceSource? get source {
    final s = widget.controller.source;
    return s is LiveWorkspaceSource && s.installedMac ? s : null;
  }

  late final Future<List<wire.ProviderDescriptor>> providers;
  late final Future<List<wire.Connection>> connections;
  final model = TextEditingController();
  final route = TextEditingController();
  final inputPrice = TextEditingController();
  final outputPrice = TextEditingController();
  final requestCeiling = TextEditingController(text: '0.02');
  final probeCeiling = TextEditingController(text: '0.02');
  final inputTokens = TextEditingController(text: '32768');
  final outputTokens = TextEditingController(text: '2048');
  wire.ProviderDescriptor? provider;
  wire.Connection? connection;
  ResourceSubmission<wire.Job>? qualification;
  wire.Job? job;
  Map<String, Object?>? trustedProfile;
  ResourceSubmission<DraftedResource>? profileDraft;
  ResourceSubmission<PlanOutcome>? plan;
  PendingSubmission? pending;
  String? pendingStep, status;
  bool busy = false, unknown = false;
  bool recoveryLoading = true;
  bool recoveryUnavailable = false;
  bool retryAvailable = false;
  Timer? poller;

  @override
  void initState() {
    super.initState();
    providers = source?.api.modelProviders() ?? Future.value(const []);
    connections = source?.api.connections() ?? Future.value(const []);
    unawaited(restoreRecovery());
  }

  @override
  void dispose() {
    poller?.cancel();
    for (final c in [
      model,
      route,
      inputPrice,
      outputPrice,
      requestCeiling,
      probeCeiling,
      inputTokens,
      outputTokens,
    ]) {
      c.dispose();
    }
    super.dispose();
  }

  Future<void> saveRecovery(Map<String, Object?> record) async {
    final store = widget.credentials;
    if (store == null) {
      throw StateError(
        'Secure storage is unavailable; no provider request was sent.',
      );
    }
    await store.writeNamed(
      store.keys.modelSetup,
      jsonEncode({'installation_id': source?.installationId, ...record}),
    );
  }

  Future<void> savePending(
    String step,
    PendingSubmission request,
    LiveWorkspaceSource s,
  ) => saveRecovery({
    'phase': 'mutation',
    'step': step,
    'submission': s.providerSetupRecoveryRecord(request),
  });

  Future<void> restoreRecovery() async {
    final s = source;
    final store = widget.credentials;
    if (s == null || store == null) {
      recoveryUnavailable = true;
      if (mounted) {
        setState(() {
          status =
              'Secure recovery storage is unavailable. Model qualification is disabled.';
          recoveryLoading = false;
        });
      } else {
        recoveryLoading = false;
      }
      return;
    }
    try {
      final raw = await store.readNamed(store.keys.modelSetup);
      if (raw == null) return;
      final decoded = jsonDecode(raw);
      if (decoded is! Map<String, Object?>) {
        recoveryUnavailable = true;
        if (mounted) {
          setState(
            () => status =
                'The saved qualification record is malformed. New probes are disabled until it is repaired.',
          );
        }
        return;
      }
      if (decoded['installation_id'] is String &&
          decoded['installation_id'] != s.installationId) {
        await store.deleteNamed(store.keys.modelSetup);
        return;
      }
      if (decoded['installation_id'] != s.installationId) {
        recoveryUnavailable = true;
        if (mounted) {
          setState(
            () => status =
                'The saved qualification record is malformed. New probes are disabled until it is repaired.',
          );
        }
        return;
      }
      final phase = decoded['phase'];
      final savedStep = phase == 'command' ? 'qualify' : decoded['step'];
      if ((phase == 'command' || phase == 'mutation') &&
          savedStep is String &&
          decoded['submission'] is Map<String, Object?>) {
        final restored = s.restoreProviderSetupSubmission(
          savedStep,
          decoded['submission'] as Map<String, Object?>,
        );
        switch (savedStep) {
          case 'qualify':
            qualification = restored as ResourceSubmission<wire.Job>;
          case 'profile_stage':
            profileDraft = restored as ResourceSubmission<DraftedResource>;
          case 'profile_plan':
            plan = restored as ResourceSubmission<PlanOutcome>;
        }
        pending = restored;
        pendingStep = savedStep;
        unknown = true;
        status = 'Recovering the original setup command from secure storage.';
        if (mounted) setState(() {});
        await checkCommand(s);
      } else if (decoded['phase'] == 'job' && decoded['job_id'] is String) {
        job = await s.api.jobGet(decoded['job_id'] as String);
        if (mounted) {
          setState(
            () => status = 'Restored qualification job ' + job!.label + '.',
          );
        }
        if (!job!.state.isTerminal) startPolling(s);
        await refreshJob(s);
      } else {
        recoveryUnavailable = true;
        if (mounted) {
          setState(
            () => status =
                'The saved qualification state is not recognized. New probes are disabled to avoid repeating a possibly sent request.',
          );
        }
      }
    } on Exception {
      recoveryUnavailable = true;
      if (mounted) {
        setState(
          () => status =
              'Saved model setup could not be recovered. No new provider request was sent.',
        );
      }
    } finally {
      recoveryLoading = false;
      if (mounted) setState(() {});
    }
  }

  Future<bool> send(
    PendingSubmission request,
    String step,
    LiveWorkspaceSource s,
  ) async {
    try {
      await s.submit(request);
      pending = null;
      pendingStep = null;
      unknown = false;
      return true;
    } on AcknowledgmentUnknown {
      setState(() {
        busy = false;
        unknown = true;
        pending = request;
        pendingStep = step;
        status =
            'Acknowledgment unknown. Check the original command before continuing.';
      });
      return false;
    } on SourceRefusal catch (e) {
      pending = null;
      pendingStep = null;
      switch (step) {
        case 'profile_stage':
          profileDraft = null;
        case 'profile_plan':
        case 'profile_apply':
          plan = null;
      }
      final store = widget.credentials;
      if (store != null) await store.deleteNamed(store.keys.modelSetup);
      fail(e);
      return false;
    } on Exception {
      setState(() {
        busy = false;
        unknown = true;
        pending = request;
        pendingStep = step;
        status =
            'The command outcome could not be confirmed. Check this same command before continuing.';
      });
      return false;
    }
  }

  Future<void> qualify(LiveWorkspaceSource s) async {
    final p = provider;
    final c = connection;
    final name = model.text.trim();
    final routeValue = route.text
        .split(',')
        .map((part) => part.trim())
        .where((part) => part.isNotEmpty)
        .toList();
    final inRate = parseMicro(inputPrice.text),
        outRate = parseMicro(outputPrice.text);
    final cost = parseMicro(requestCeiling.text),
        probe = parseMicro(probeCeiling.text);
    final maxIn = int.tryParse(inputTokens.text),
        maxOut = int.tryParse(outputTokens.text);
    if (p == null ||
        c == null ||
        c.validationState != wire.ConnectionValidationState.valid) {
      setState(() => status = 'Choose a validated provider connection first.');
      return;
    }
    if (recoveryUnavailable) {
      setState(
        () => status =
            'New probes are disabled because the prior qualification outcome cannot be reconciled.',
      );
      return;
    }
    if (name.isEmpty ||
        (p.id == 'openrouter' && routeValue.isEmpty) ||
        inRate == null ||
        outRate == null ||
        cost == null ||
        probe == null ||
        maxIn == null ||
        maxOut == null ||
        maxIn < 1 ||
        maxOut < 1 ||
        cost < 1 ||
        probe < 1) {
      setState(
        () => status =
            'Enter the model, prices, token limits, and positive spend ceilings.',
      );
      return;
    }
    setState(() {
      busy = true;
      status = 'Submitting one bounded qualification request…';
    });
    try {
      qualification = s.prepareExecutionProfileQualification(
        definition: candidate(
          p,
          c,
          name,
          routeValue,
          inRate,
          outRate,
          cost,
          maxIn,
          maxOut,
        ),
        qualificationCostBound: wire.Money(currency: 'USD', microUnits: probe),
      );
      await savePending('qualify', qualification!, s);
      if (!await send(qualification!, 'qualify', s)) return;
      job = qualification!.result;
      if (job == null) throw StateError('No durable job was returned.');
      await saveRecovery({'phase': 'job', 'job_id': job!.id});
      setState(() {
        busy = false;
        status = 'Qualification job is ' + job!.label + '.';
      });
      startPolling(s);
      await refreshJob(s);
    } on Exception catch (e) {
      fail(e);
    }
  }

  Map<String, Object?> candidate(
    wire.ProviderDescriptor p,
    wire.Connection c,
    String name,
    List<String> routeValue,
    int inRate,
    int outRate,
    int cost,
    int maxIn,
    int maxOut,
  ) {
    final endpoint = p.defaultEndpoint;
    final routing = switch (p.id) {
      'openai' => <String, Object?>{},
      'openrouter' => <String, Object?>{
        'only': routeValue,
        'allow_fallbacks': false,
        'require_parameters': true,
        'price_ceiling': <String, Object?>{
          'currency': 'USD',
          'input_per_million': usdFromMicro(inRate),
          'output_per_million': usdFromMicro(outRate),
        },
      },
      'experiential' => <String, Object?>{
        'gateway': <String, Object?>{
          'retry': <String, Object?>{
            'max_attempts_per_route': 1,
            'max_total_attempts': 1,
          },
          'backoff': <String, Object?>{'type': 'none'},
          'routing': <String, Object?>{'allow_fallbacks': false},
        },
        if (routeValue.isNotEmpty) 'route_id': routeValue.first,
      },
      _ => <String, Object?>{},
    };
    final adapter = <String, Object?>{
      'schema': 'zatiti.responses/v2',
      'provider': p.id,
      'endpoint': endpoint,
      'model': name,
      'connection_id': c.id,
      'session_mode': p.sessionMode,
      'routing': routing,
      'max_input_tokens': maxIn,
      'max_output_tokens': maxOut,
      'max_response_bytes': 1048576,
      'timeout_seconds': 90,
      'currency': 'USD',
      'input_rate': <String, Object?>{
        'numerator_micro_units': inRate,
        'denominator_units': 1000000,
        'unit': 'input_token',
      },
      'output_rate': <String, Object?>{
        'numerator_micro_units': outRate,
        'denominator_units': 1000000,
        'unit': 'output_token',
      },
      'enforcement': <String, Object?>{
        'cost': p.id == 'experiential' ? 'advisory' : 'enforced',
        'disclosure': 'enforced',
        'maximum_cost': <String, Object?>{
          'currency': 'USD',
          'micro_units': cost,
        },
        'provider_destinations': [endpoint],
      },
    };
    return <String, Object?>{
      'executor': 'hosted',
      'model': name,
      'connection_id': c.id,
      'provider_destination': endpoint,
      'capabilities': <String>[],
      'cost_bound': <String, Object?>{'currency': 'USD', 'micro_units': cost},
      'classification': 'internal',
      'context_capture': 'complete',
      'adapter_profile': adapter,
      'connection_version': c.version,
    };
  }

  void startPolling(LiveWorkspaceSource s) {
    poller?.cancel();
    poller = Timer.periodic(const Duration(seconds: 3), (_) {
      if (mounted && !busy) refreshJob(s);
    });
  }

  Future<void> refreshJob(LiveWorkspaceSource s) async {
    final current = job;
    if (current == null) return;
    try {
      final next = await s.api.jobGet(current.id);
      if (!mounted) return;
      setState(() {
        job = next;
        status = switch (next.state) {
          wire.JobState.pending =>
            'Queued. The provider request will not be replayed.',
          wire.JobState.running => 'Running; observing the same durable job.',
          wire.JobState.succeeded =>
            'Provider response verified. Review before profile creation.',
          wire.JobState.failed =>
            'Qualification failed; this profile cannot be activated.',
          wire.JobState.outcomeUnknown =>
            'Provider outcome unknown. The probe will not be resent.',
          wire.JobState.cancelled => 'Qualification cancelled.',
        };
        if (next.state == wire.JobState.succeeded) {
          trustedProfile = decodeProfile(next.result);
          poller?.cancel();
        }
        if (next.state.isTerminal) poller?.cancel();
      });
    } on Exception {
      if (mounted) {
        setState(
          () => status =
              'Job read unavailable. Retrying the read only; no provider call is repeated.',
        );
      }
    }
  }

  Future<void> resetTerminalQualification() async {
    final state = job?.state;
    if (state != wire.JobState.failed && state != wire.JobState.cancelled) {
      return;
    }
    try {
      final store = widget.credentials;
      if (store != null) await store.deleteNamed(store.keys.modelSetup);
      if (!mounted) return;
      setState(() {
        qualification = null;
        job = null;
        trustedProfile = null;
        profileDraft = null;
        plan = null;
        pending = null;
        pendingStep = null;
        unknown = false;
        recoveryUnavailable = false;
        status =
            'The previous qualification is terminal. You can revise the model settings and submit a new bounded probe.';
      });
    } on Exception catch (e) {
      fail(e);
    }
  }

  Map<String, Object?>? decodeProfile(Object? result) {
    if (result is! Map<String, Object?> ||
        result['resource'] is! Map<String, Object?>) {
      return null;
    }
    final raw = result['resource'] as Map<String, Object?>;
    const fields = {
      'executor',
      'model',
      'connection_id',
      'provider_destination',
      'capabilities',
      'cost_bound',
      'classification',
      'context_capture',
      'adapter_profile',
      'connection_version',
    };
    if (raw.keys.toSet().difference(fields).isNotEmpty ||
        !raw.keys.toSet().containsAll(fields) ||
        raw['executor'] != 'hosted' ||
        raw['model'] is! String ||
        raw['connection_id'] is! String ||
        raw['provider_destination'] is! String ||
        raw['capabilities'] is! List<Object?> ||
        raw['classification'] != 'internal' ||
        raw['context_capture'] != 'complete' ||
        raw['connection_version'] is! int ||
        raw['adapter_profile'] is! Map<String, Object?>) {
      return null;
    }
    try {
      wire.Money.fromJson(raw['cost_bound']);
      final adapter = raw['adapter_profile'] as Map<String, Object?>;
      if (adapter['schema'] != 'zatiti.responses/v2' ||
          adapter['model'] != raw['model'] ||
          adapter['capability_evidence'] is! Map<String, Object?>) {
        return null;
      }
      return Map<String, Object?>.from(raw);
    } on Exception {
      return null;
    }
  }

  Future<void> createProfile(LiveWorkspaceSource s) async {
    final profile = trustedProfile;
    if (profile == null) return;
    setState(() {
      busy = true;
      status = 'Staging the controller-qualified profile…';
    });
    try {
      profileDraft = s.prepareExecutionProfileCreate(profile);
      await savePending('profile_stage', profileDraft!, s);
      if (!await send(profileDraft!, 'profile_stage', s)) return;
      await planProfile(s);
    } on Exception catch (e) {
      fail(e);
    }
  }

  Future<void> planProfile(LiveWorkspaceSource s) async {
    try {
      final d = profileDraft?.result;
      if (d == null) throw StateError('Profile draft unavailable.');
      plan = s.preparePlan(draftId: d.draftId, expectedVersion: d.draftVersion);
      setState(() {
        busy = true;
        status = 'Checking profile and authority requirements…';
      });
      await savePending('profile_plan', plan!, s);
      if (!await send(plan!, 'profile_plan', s)) return;
      setState(() {
        busy = false;
        status = plan!.result!.isClean
            ? 'Review the plan, then apply to enable this profile.'
            : 'Controller plan has unmet requirements.';
      });
    } on Exception catch (e) {
      fail(e);
    }
  }

  Future<void> applyProfile(LiveWorkspaceSource s) async {
    final reviewed = plan?.result;
    if (reviewed == null || !reviewed.isClean) return;
    setState(() {
      busy = true;
      status = 'Applying the reviewed profile plan…';
    });
    try {
      final apply = s.prepareApplyPlan(reviewed);
      await savePending('profile_apply', apply, s);
      if (!await send(apply, 'profile_apply', s)) return;
      await widget.controller.reconnect();
      final store = widget.credentials;
      if (store != null) await store.deleteNamed(store.keys.modelSetup);
      if (mounted) Navigator.of(context).pop(true);
    } on Exception catch (e) {
      fail(e);
    }
  }

  Future<void> checkCommand(LiveWorkspaceSource s) async {
    final request = pending;
    final step = pendingStep;
    if (request == null || step == null) return;
    setState(() {
      busy = true;
      status = 'Checking the original command…';
    });
    try {
      switch (await s.resolve(request)) {
        case ResolvedAcknowledged():
          pending = null;
          pendingStep = null;
          unknown = false;
          if (step == 'qualify') {
            job = qualification?.result;
            if (job == null) {
              recoveryUnavailable = true;
              setState(() {
                busy = false;
                status =
                    'Acknowledged, but no job identity is available. Do not submit another probe.';
              });
            } else {
              await saveRecovery({'phase': 'job', 'job_id': job!.id});
              setState(() => busy = false);
              startPolling(s);
              await refreshJob(s);
            }
          } else if (step == 'profile_stage') {
            if (profileDraft?.result == null) {
              recoveryUnavailable = true;
              setState(() {
                busy = false;
                status =
                    'The profile draft acknowledgment has no decoded resource. Do not create another draft.';
              });
              break;
            }
            await planProfile(s);
          } else if (step == 'profile_plan') {
            setState(() {
              busy = false;
              status = plan?.result?.isClean == true
                  ? 'Review and apply the profile plan.'
                  : 'Controller plan has unmet requirements.';
            });
          } else {
            final store = widget.credentials;
            if (store != null) await store.deleteNamed(store.keys.modelSetup);
            await widget.controller.reconnect();
            if (mounted) Navigator.of(context).pop(true);
          }
          break;
        case ResolvedRefused(:final refusal):
          pending = null;
          pendingStep = null;
          if (step == 'profile_stage') profileDraft = null;
          if (step == 'profile_plan' || step == 'profile_apply') plan = null;
          final store = widget.credentials;
          if (store != null) await store.deleteNamed(store.keys.modelSetup);
          fail(refusal);
          break;
        case ResolvedNotReceived():
          setState(() {
            busy = false;
            unknown = false;
            retryAvailable = true;
            status =
                'Controller confirms this command was not received. Retry the same saved command to continue safely.';
          });
          break;
      }
    } on Exception catch (e) {
      fail(e);
    }
  }

  Future<void> retryCommand(LiveWorkspaceSource s) async {
    final request = pending;
    final step = pendingStep;
    if (request == null || step == null || !retryAvailable) return;
    setState(() {
      busy = true;
      retryAvailable = false;
      status = 'Retrying the same saved command…';
    });
    try {
      await savePending(step, request, s);
      await s.submit(request);
      pending = null;
      pendingStep = null;
      unknown = false;
      switch (step) {
        case 'qualify':
          job = qualification?.result;
          if (job == null) {
            recoveryUnavailable = true;
            setState(() {
              busy = false;
              status =
                  'The acknowledged command returned no job identity. Do not submit another probe.';
            });
            return;
          }
          await saveRecovery({'phase': 'job', 'job_id': job!.id});
          setState(() {
            busy = false;
            status = 'Qualification job is ' + job!.label + '.';
          });
          startPolling(s);
          await refreshJob(s);
        case 'profile_stage':
          await planProfile(s);
        case 'profile_plan':
          setState(() {
            busy = false;
            status = plan?.result?.isClean == true
                ? 'Review and apply the profile plan.'
                : 'Controller plan has unmet requirements.';
          });
        case 'profile_apply':
          final store = widget.credentials;
          if (store != null) await store.deleteNamed(store.keys.modelSetup);
          await widget.controller.reconnect();
          if (mounted) Navigator.of(context).pop(true);
      }
    } on AcknowledgmentUnknown {
      setState(() {
        busy = false;
        unknown = true;
        status = 'Acknowledgment unknown. Check the original command.';
      });
    } on SourceRefusal catch (e) {
      final store = widget.credentials;
      if (store != null) await store.deleteNamed(store.keys.modelSetup);
      pending = null;
      pendingStep = null;
      fail(e);
    } on Exception {
      setState(() {
        busy = false;
        unknown = true;
        status =
            'The retry outcome could not be confirmed. Check this same command before continuing.';
      });
    }
  }

  int? parseMicro(String text) {
    final m = RegExp(
      r'^(0|[1-9][0-9]*)(?:\.([0-9]{1,6}))?$',
    ).firstMatch(text.trim());
    if (m == null) return null;
    final whole = int.tryParse(m.group(1)!);
    final frac = int.tryParse((m.group(2) ?? '').padRight(6, '0'));
    if (whole == null || frac == null || whole > 9000000000000) return null;
    return whole * 1000000 + frac;
  }

  String usdFromMicro(int micro) {
    final whole = micro ~/ 1000000;
    final fraction = (micro % 1000000).toString().padLeft(6, '0');
    final trimmed = fraction.replaceFirst(RegExp(r'0+$'), '');
    return trimmed.isEmpty ? whole.toString() : '$whole.$trimmed';
  }

  void fail(Object e) {
    if (!mounted) return;
    setState(() {
      busy = false;
      status = e is SourceRefusal
          ? 'Controller refused: ' + e.message
          : 'Setup failed: ' + e.toString();
    });
  }

  Widget field(
    TextEditingController c,
    String label, {
    String? helper,
    bool decimal = true,
  }) => Padding(
    padding: const EdgeInsets.only(top: 8),
    child: TextField(
      controller: c,
      enabled: !busy && !recoveryLoading && !recoveryUnavailable && job == null,
      keyboardType: TextInputType.numberWithOptions(decimal: decimal),
      decoration: InputDecoration(labelText: label, helperText: helper),
    ),
  );

  @override
  Widget build(BuildContext context) {
    final s = source;
    if (s == null) {
      return const AlertDialog(
        title: Text('Set up a model'),
        content: Text('Requires the installed Mac controller.'),
      );
    }
    final reviewed = plan?.result;
    return AlertDialog(
      title: const Text('Set up a model'),
      content: SizedBox(
        width: 560,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                'Select a validated provider connection and enter its model ID, published prices, limits, and maximum probe spend.',
              ),
              FutureBuilder<List<wire.ProviderDescriptor>>(
                future: providers,
                builder: (context, snap) {
                  if (!snap.hasData) return const LinearProgressIndicator();
                  final xs = snap.data!;
                  return DropdownButtonFormField<String>(
                    value: provider?.id,
                    decoration: const InputDecoration(labelText: 'Provider'),
                    items: [
                      for (final p in xs)
                        DropdownMenuItem(
                          value: p.id,
                          child: Text(p.displayName),
                        ),
                    ],
                    onChanged:
                        busy ||
                            recoveryLoading ||
                            recoveryUnavailable ||
                            job != null
                        ? null
                        : (id) => setState(() {
                            provider = xs.where((p) => p.id == id).firstOrNull;
                            connection = null;
                          }),
                  );
                },
              ),
              FutureBuilder<List<wire.Connection>>(
                future: connections,
                builder: (context, snap) {
                  if (!snap.hasData) return const LinearProgressIndicator();
                  final xs = snap.data!
                      .where(
                        (c) =>
                            c.validationState ==
                                wire.ConnectionValidationState.valid &&
                            (provider == null || c.provider == provider!.id),
                      )
                      .toList();
                  return DropdownButtonFormField<String>(
                    value: connection?.id,
                    decoration: const InputDecoration(
                      labelText: 'Validated account connection',
                    ),
                    items: [
                      for (final c in xs)
                        DropdownMenuItem(
                          value: c.id,
                          child: Text(c.provider + ' · ' + c.accountIdentity),
                        ),
                    ],
                    onChanged:
                        busy ||
                            recoveryLoading ||
                            recoveryUnavailable ||
                            job != null
                        ? null
                        : (id) => setState(
                            () => connection = xs
                                .where((c) => c.id == id)
                                .firstOrNull,
                          ),
                  );
                },
              ),
              field(
                model,
                'Model ID',
                helper: 'For example, z-ai/glm-flash-latest',
                decimal: false,
              ),
              if (provider?.id != null && provider!.id != 'openai')
                field(
                  route,
                  provider!.id == 'openrouter'
                      ? 'OpenRouter provider route(s)'
                      : 'Experiential route ID (optional)',
                  helper: provider!.id == 'openrouter'
                      ? 'Use provider names from the selected model’s routing options.'
                      : 'Leave blank to use the provider’s default route.',
                  decimal: false,
                ),
              field(inputPrice, 'Input price (USD per million tokens)'),
              field(outputPrice, 'Output price (USD per million tokens)'),
              field(inputTokens, 'Maximum input tokens', decimal: false),
              field(outputTokens, 'Maximum output tokens', decimal: false),
              field(requestCeiling, 'Maximum cost per model request (USD)'),
              field(probeCeiling, 'Maximum qualification spend (USD)'),
              if (provider?.id == 'experiential')
                const Padding(
                  padding: EdgeInsets.only(top: 8),
                  child: Text(
                    'Experiential does not document a hard provider-side price ceiling. Zatiti will label model cost advisory; provider charges may exceed the estimate.',
                  ),
                ),
              if (job != null)
                Text('Qualification job ' + job!.id + ': ' + job!.label),
              if (trustedProfile != null)
                ..._qualifiedProfileSummary(trustedProfile!),
              if (reviewed != null) ...[
                Text(
                  reviewed.isClean
                      ? 'Plan ready for review.'
                      : 'Plan needs attention.',
                ),
                for (final d in reviewed.diagnostics) Text('• ' + d),
                for (final r in reviewed.pendingRequirements) Text('• ' + r),
              ],
              if (status != null)
                Padding(
                  padding: const EdgeInsets.only(top: 12),
                  child: Semantics(liveRegion: true, child: Text(status!)),
                ),
              if (busy || recoveryLoading) const LinearProgressIndicator(),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: busy ? null : () => Navigator.of(context).pop(),
          child: const Text('Close'),
        ),
        if (unknown)
          OutlinedButton(
            onPressed: busy || recoveryLoading ? null : () => checkCommand(s),
            child: const Text('Check command'),
          ),
        if (retryAvailable)
          OutlinedButton(
            onPressed: busy || recoveryLoading ? null : () => retryCommand(s),
            child: const Text('Retry same command'),
          ),
        if (job != null && !job!.state.isTerminal)
          OutlinedButton(
            onPressed: busy || recoveryLoading ? null : () => refreshJob(s),
            child: const Text('Check job'),
          ),
        if (job?.state == wire.JobState.failed ||
            job?.state == wire.JobState.cancelled)
          OutlinedButton(
            onPressed: busy || recoveryLoading
                ? null
                : resetTerminalQualification,
            child: const Text('Revise model settings'),
          ),
        if (job == null &&
            trustedProfile == null &&
            pending == null &&
            !recoveryUnavailable)
          FilledButton(
            onPressed: busy || recoveryLoading ? null : () => qualify(s),
            child: const Text('Qualify model'),
          ),
        if (job?.state == wire.JobState.succeeded &&
            trustedProfile != null &&
            profileDraft == null)
          FilledButton(
            onPressed: busy || recoveryLoading ? null : () => createProfile(s),
            child: const Text('Create profile draft'),
          ),
        if (profileDraft?.result != null &&
            plan == null &&
            pending == null &&
            !recoveryUnavailable)
          FilledButton(
            onPressed: busy || recoveryLoading ? null : () => planProfile(s),
            child: const Text('Check profile plan again'),
          ),
        if (reviewed?.isClean == true)
          FilledButton(
            onPressed: busy || recoveryLoading ? null : () => applyProfile(s),
            child: const Text('Apply reviewed plan'),
          ),
      ],
    );
  }

  List<Widget> _qualifiedProfileSummary(Map<String, Object?> profile) {
    final adapter = profile['adapter_profile'] as Map<String, Object?>;
    final evidence = adapter['capability_evidence'] as Map<String, Object?>;
    final cost = wire.Money.fromJson(profile['cost_bound']);
    final limitations = evidence['limitations'] is List<Object?>
        ? (evidence['limitations'] as List<Object?>).whereType<String>()
        : const <String>[];
    return [
      Text('Verified model: ' + profile['model'].toString()),
      Text('Route: ' + profile['provider_destination'].toString()),
      Text('Maximum request spend: ' + cost.format()),
      Text('Context capture: ' + profile['context_capture'].toString()),
      for (final limitation in limitations)
        Text('Qualification limit: ' + limitation),
    ];
  }
}

extension on wire.JobState {
  bool get isTerminal => switch (this) {
    wire.JobState.succeeded ||
    wire.JobState.failed ||
    wire.JobState.outcomeUnknown ||
    wire.JobState.cancelled => true,
    wire.JobState.pending || wire.JobState.running => false,
  };
}
