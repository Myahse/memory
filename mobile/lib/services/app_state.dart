import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:supabase_flutter/supabase_flutter.dart';

import '../models/memory.dart';
import 'api.dart';
import 'sync_queue.dart';

/// App-wide services. Kept deliberately simple: a few ChangeNotifiers.
class AppState {
  AppState._();
  static final instance = AppState._();

  final api = Api();
  late final SyncQueue queue = SyncQueue(api);
  final theme = ThemeController();
  final recent = RecentMemories();
  final messengerKey = GlobalKey<ScaffoldMessengerState>();
  RealtimeChannel? _channel;

  Future<void> init() async {
    await theme.load();
    await queue.init();
    queue.onSynced.listen((_) => recent.refresh());
    Supabase.instance.client.auth.onAuthStateChange.listen((data) {
      if (data.session != null) {
        _subscribe(data.session!.user.id);
        recent.refresh();
        queue.sync();
      } else {
        _unsubscribe();
        recent.clear();
      }
    });
  }

  /// Live processing status → "✓ Memory ready" notifications.
  void _subscribe(String userId) {
    _unsubscribe();
    _channel = Supabase.instance.client
        .channel('memories:$userId')
        .onPostgresChanges(
          event: PostgresChangeEvent.update,
          schema: 'public',
          table: 'memories',
          filter: PostgresChangeFilter(type: PostgresChangeFilterType.eq, column: 'user_id', value: userId),
          callback: (payload) {
            final status = payload.newRecord['status'];
            if (status != payload.oldRecord['status']) {
              if (status == 'ready') {
                final title = payload.newRecord['title'] as String?;
                toast('✓ Memory ready${title == null ? '' : ': $title'}');
              } else if (status == 'failed') {
                toast('Processing failed — open the memory to retry.');
              }
            }
            recent.refresh();
          },
        )
        .subscribe();
  }

  void _unsubscribe() {
    if (_channel != null) {
      Supabase.instance.client.removeChannel(_channel!);
      _channel = null;
    }
  }

  void toast(String message) {
    messengerKey.currentState
      ?..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message), behavior: SnackBarBehavior.floating));
  }
}

class ThemeController extends ChangeNotifier {
  ThemeMode mode = ThemeMode.system;

  Future<void> load() async {
    final p = await SharedPreferences.getInstance();
    mode = ThemeMode.values.byName(p.getString('theme') ?? 'system');
    notifyListeners();
  }

  Future<void> set(ThemeMode m) async {
    mode = m;
    notifyListeners();
    final p = await SharedPreferences.getInstance();
    await p.setString('theme', m.name);
  }
}

/// Recent memories with an offline cache so Home works without network.
class RecentMemories extends ChangeNotifier {
  List<Memory> items = [];
  String next = '';
  bool loading = false;
  String? error;
  bool fromCache = false;
  Timer? _poll;

  Future<void> refresh() async {
    loading = items.isEmpty;
    notifyListeners();
    try {
      final res = await AppState.instance.api.listMemories();
      items = res.memories;
      next = res.next;
      error = null;
      fromCache = false;
      final p = await SharedPreferences.getInstance();
      await p.setString('recent_cache', jsonEncode(items.map((m) => m.toJson()).toList()));
    } on ApiException catch (e) {
      error = e.message;
      if (items.isEmpty) await _loadCache();
    } finally {
      loading = false;
      notifyListeners();
      _schedulePoll();
    }
  }

  Future<void> loadMore() async {
    if (next.isEmpty) return;
    try {
      final res = await AppState.instance.api.listMemories(before: next);
      items = [...items, ...res.memories];
      next = res.next;
      notifyListeners();
    } on ApiException catch (e) {
      AppState.instance.toast(e.message);
    }
  }

  // Fallback to Realtime: poll while something is processing.
  void _schedulePoll() {
    _poll?.cancel();
    if (items.any((m) => m.isProcessing)) {
      _poll = Timer(const Duration(seconds: 6), refresh);
    }
  }

  Future<void> _loadCache() async {
    final p = await SharedPreferences.getInstance();
    final raw = p.getString('recent_cache');
    if (raw == null) return;
    items = (jsonDecode(raw) as List).map((e) => Memory.fromJson((e as Map).cast<String, dynamic>())).toList();
    fromCache = true;
  }

  void remove(String id) {
    items = items.where((m) => m.id != id).toList();
    notifyListeners();
  }

  Future<void> clear() async {
    _poll?.cancel();
    items = [];
    next = '';
    final p = await SharedPreferences.getInstance();
    await p.remove('recent_cache');
    notifyListeners();
  }
}
