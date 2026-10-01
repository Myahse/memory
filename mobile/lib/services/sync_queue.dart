import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:connectivity_plus/connectivity_plus.dart';
import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:path_provider/path_provider.dart';
import 'package:uuid/uuid.dart';

import '../models/memory.dart';
import 'api.dart';

enum QueueState { waiting, uploading, duplicate, failed }

/// A capture that has not reached the cloud yet.
class QueueItem {
  QueueItem({
    required this.id,
    required this.type,
    required this.createdAt,
    this.title,
    this.content,
    this.sourceUrl,
    this.filePath,
    this.mime,
    this.size = 0,
    this.hash,
    this.capturedAt,
    this.metadata = const {},
    this.state = QueueState.waiting,
    this.error,
    this.memoryId,
    this.duplicates = const [],
    this.onDuplicate,
    this.replaceId,
  });

  final String id; // also the server-side client_id → retries are idempotent
  final String type;
  final DateTime createdAt;
  final String? title;
  final String? content;
  final String? sourceUrl;
  final String? filePath;
  final String? mime;
  final int size;
  final String? hash;
  final DateTime? capturedAt;
  final Map<String, dynamic> metadata;
  QueueState state;
  String? error;
  String? memoryId;
  List<Memory> duplicates;
  String? onDuplicate;
  String? replaceId;

  String get displayTitle {
    if ((title ?? '').isNotEmpty) return title!;
    if (type == 'note' && (content ?? '').isNotEmpty) return content!.length > 60 ? '${content!.substring(0, 60)}…' : content!;
    if (type == 'link') return sourceUrl ?? 'Link';
    return MemoryType.label(type);
  }

  Map<String, dynamic> toJson() => {
        'id': id,
        'type': type,
        'created_at': createdAt.toIso8601String(),
        'title': title,
        'content': content,
        'source_url': sourceUrl,
        'file_path': filePath,
        'mime': mime,
        'size': size,
        'hash': hash,
        'captured_at': capturedAt?.toIso8601String(),
        'metadata': metadata,
        'state': state == QueueState.uploading ? QueueState.waiting.name : state.name,
        'error': error,
        'memory_id': memoryId,
        'duplicates': duplicates.map((d) => d.toJson()).toList(),
        'on_duplicate': onDuplicate,
        'replace_id': replaceId,
      };

  factory QueueItem.fromJson(Map<String, dynamic> j) => QueueItem(
        id: j['id'] as String,
        type: j['type'] as String,
        createdAt: DateTime.parse(j['created_at'] as String),
        title: j['title'] as String?,
        content: j['content'] as String?,
        sourceUrl: j['source_url'] as String?,
        filePath: j['file_path'] as String?,
        mime: j['mime'] as String?,
        size: (j['size'] as num?)?.toInt() ?? 0,
        hash: j['hash'] as String?,
        capturedAt: j['captured_at'] == null ? null : DateTime.tryParse(j['captured_at'] as String),
        metadata: (j['metadata'] as Map?)?.cast<String, dynamic>() ?? const {},
        state: QueueState.values.byName(j['state'] as String? ?? 'waiting'),
        error: j['error'] as String?,
        memoryId: j['memory_id'] as String?,
        duplicates: ((j['duplicates'] as List?) ?? const [])
            .map((e) => Memory.fromJson((e as Map).cast<String, dynamic>()))
            .toList(),
        onDuplicate: j['on_duplicate'] as String?,
        replaceId: j['replace_id'] as String?,
      );
}

/// Offline-first capture queue.
///
/// Every capture (photo, voice, note, document, link) is first written to
/// local storage, so it works on a plane. The queue then uploads in the
/// background whenever connectivity is available; the server does all the
/// heavy processing.
class SyncQueue extends ChangeNotifier {
  SyncQueue(this.api);

  final Api api;
  final List<QueueItem> _items = [];
  late Directory _dir;
  StreamSubscription<List<ConnectivityResult>>? _connSub;
  Timer? _timer;
  bool _running = false;
  bool online = true;

  /// Called when an item finished uploading (used to refresh the home list).
  final _synced = StreamController<Memory>.broadcast();
  Stream<Memory> get onSynced => _synced.stream;

  List<QueueItem> get items => List.unmodifiable(_items);
  int get pendingCount => _items.length;

  Future<void> init() async {
    final docs = await getApplicationDocumentsDirectory();
    _dir = Directory('${docs.path}/memory_queue');
    await _dir.create(recursive: true);
    final index = File('${_dir.path}/queue.json');
    if (await index.exists()) {
      try {
        final list = jsonDecode(await index.readAsString()) as List;
        _items.addAll(list.map((e) => QueueItem.fromJson((e as Map).cast<String, dynamic>())));
      } catch (e) {
        debugPrint('queue index unreadable: $e');
      }
    }
    final conn = Connectivity();
    online = !(await conn.checkConnectivity()).contains(ConnectivityResult.none);
    _connSub = conn.onConnectivityChanged.listen((r) {
      final was = online;
      online = !r.contains(ConnectivityResult.none);
      notifyListeners();
      if (online && !was) unawaited(sync());
    });
    _timer = Timer.periodic(const Duration(seconds: 30), (_) => sync());
    notifyListeners();
    unawaited(sync());
  }

  @override
  void dispose() {
    _connSub?.cancel();
    _timer?.cancel();
    _synced.close();
    super.dispose();
  }

  Future<void> _persist() async {
    final index = File('${_dir.path}/queue.json');
    final tmp = File('${index.path}.tmp');
    await tmp.writeAsString(jsonEncode(_items.map((e) => e.toJson()).toList()));
    await tmp.rename(index.path);
  }

  // ---- Capture -------------------------------------------------------------

  /// Copies [source] into the queue directory (the picker's temp file may vanish).
  Future<void> addFile(File source, {required String type, required String mime, Map<String, dynamic> metadata = const {}, DateTime? capturedAt}) async {
    final id = const Uuid().v4();
    final ext = source.path.contains('.') ? source.path.substring(source.path.lastIndexOf('.')) : '';
    final dest = await source.copy('${_dir.path}/$id$ext');
    final digest = await sha256.bind(dest.openRead()).first;
    _items.insert(
      0,
      QueueItem(
        id: id,
        type: type,
        createdAt: DateTime.now(),
        filePath: dest.path,
        mime: mime,
        size: await dest.length(),
        hash: digest.toString(),
        capturedAt: capturedAt,
        metadata: metadata,
      ),
    );
    await _persist();
    notifyListeners();
    unawaited(sync());
  }

  Future<void> addNote(String content, {String? title}) => _addSimple(QueueItem(
      id: const Uuid().v4(), type: 'note', createdAt: DateTime.now(), content: content, title: title));

  Future<void> addLink(String url, {String? note}) => _addSimple(QueueItem(
      id: const Uuid().v4(), type: 'link', createdAt: DateTime.now(), sourceUrl: url, content: note));

  Future<void> _addSimple(QueueItem item) async {
    _items.insert(0, item);
    await _persist();
    notifyListeners();
    unawaited(sync());
  }

  // ---- User decisions ---------------------------------------------------------

  Future<void> resolveDuplicate(QueueItem item, String choice) async {
    if (choice == 'cancel') return remove(item);
    item
      ..onDuplicate = choice
      ..replaceId = choice == 'replace' && item.duplicates.isNotEmpty ? item.duplicates.first.id : null
      ..state = QueueState.waiting
      ..error = null;
    await _persist();
    notifyListeners();
    unawaited(sync());
  }

  Future<void> retry(QueueItem item) async {
    item
      ..state = QueueState.waiting
      ..error = null;
    await _persist();
    notifyListeners();
    unawaited(sync());
  }

  Future<void> remove(QueueItem item) async {
    _items.remove(item);
    if (item.filePath != null) {
      final f = File(item.filePath!);
      if (await f.exists()) await f.delete();
    }
    await _persist();
    notifyListeners();
  }

  /// Drop everything local (used by "Delete my data" and sign-out).
  Future<void> clear() async {
    for (final i in List.of(_items)) {
      await remove(i);
    }
  }

  // ---- Sync ---------------------------------------------------------------------

  Future<void> sync() async {
    if (_running || !online) return;
    _running = true;
    try {
      for (final item in List.of(_items)) {
        if (item.state != QueueState.waiting) continue;
        final ok = await _syncOne(item);
        if (!ok && !online) break;
      }
    } finally {
      _running = false;
    }
  }

  Future<bool> _syncOne(QueueItem item) async {
    item.state = QueueState.uploading;
    notifyListeners();
    try {
      UploadTarget? target;
      if (item.memoryId == null) {
        final res = await api.createMemory({
          'type': item.type,
          'client_id': item.id,
          if ((item.title ?? '').isNotEmpty) 'title': item.title,
          if (item.content != null) 'content': item.content,
          if (item.sourceUrl != null) 'source_url': item.sourceUrl,
          if (item.mime != null) 'mime_type': item.mime,
          if (item.filePath != null) 'file_size': item.size,
          if (item.hash != null) 'content_hash': item.hash,
          if (item.capturedAt != null) 'captured_at': item.capturedAt!.toUtc().toIso8601String(),
          if (item.metadata.isNotEmpty) 'metadata': item.metadata,
          if (item.onDuplicate != null) 'on_duplicate': item.onDuplicate,
          if (item.replaceId != null) 'replace_id': item.replaceId,
        });
        item.memoryId = res.memory.id;
        target = res.upload;
        await _persist();
        if (item.filePath == null) {
          _done(item, res.memory);
          return true;
        }
      }
      target ??= await api.uploadUrl(item.memoryId!);
      await api.putFile(target, File(item.filePath!), item.mime!);
      final memory = await api.uploadComplete(item.memoryId!);
      _done(item, memory);
      return true;
    } on ApiException catch (e) {
      if (e.isNetwork) {
        item.state = QueueState.waiting;
        online = false;
      } else if (e.isDuplicate) {
        item
          ..state = QueueState.duplicate
          ..duplicates = ((e.body['duplicates'] as List?) ?? const [])
              .map((d) => Memory.fromJson((d as Map).cast<String, dynamic>()))
              .toList();
      } else if (e.status == 401) {
        item.state = QueueState.waiting; // retried after the user signs in again
      } else {
        item
          ..state = QueueState.failed
          ..error = e.message;
      }
    } catch (e) {
      item
        ..state = QueueState.failed
        ..error = 'Upload failed: $e';
    }
    await _persist();
    notifyListeners();
    return false;
  }

  void _done(QueueItem item, Memory memory) {
    unawaited(remove(item));
    _synced.add(memory);
  }
}
