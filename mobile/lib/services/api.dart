import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;
import 'package:supabase_flutter/supabase_flutter.dart';

import '../config.dart';
import '../models/memory.dart';

class ApiException implements Exception {
  ApiException(this.status, this.code, this.message, [this.body = const {}]);
  final int status;
  final String code;
  final String message;
  final Map<String, dynamic> body;

  bool get isNetwork => status == 0;
  bool get isDuplicate => code == 'duplicate';

  @override
  String toString() => message;
}

class UploadTarget {
  UploadTarget(this.url, this.path);
  final String url;
  final String path;
}

/// Thin client for the Memory Go API. Every call carries the user's Supabase
/// access token; authorization is enforced server-side (and by RLS).
class Api {
  Api({http.Client? client}) : _http = client ?? http.Client();
  final http.Client _http;

  Future<Map<String, dynamic>> _req(String method, String path, {Object? body, bool auth = true}) async {
    final headers = <String, String>{'Accept': 'application/json'};
    if (body != null) headers['Content-Type'] = 'application/json';
    if (auth) {
      final token = Supabase.instance.client.auth.currentSession?.accessToken;
      if (token == null) throw ApiException(401, 'unauthenticated', 'Please sign in again.');
      headers['Authorization'] = 'Bearer $token';
    }
    final req = http.Request(method, Uri.parse('${Config.apiUrl}$path'))..headers.addAll(headers);
    if (body != null) req.body = jsonEncode(body);
    http.Response res;
    try {
      res = await http.Response.fromStream(await _http.send(req).timeout(const Duration(seconds: 90)));
    } on SocketException {
      throw ApiException(0, 'network', 'You appear to be offline.');
    } on TimeoutException {
      throw ApiException(0, 'network', 'The request timed out. Check your connection.');
    } on http.ClientException {
      throw ApiException(0, 'network', 'You appear to be offline.');
    }
    if (res.statusCode == 204) return const {};
    Map<String, dynamic> json = const {};
    try {
      json = (jsonDecode(utf8.decode(res.bodyBytes)) as Map).cast<String, dynamic>();
    } catch (_) {}
    if (res.statusCode >= 400) {
      final err = (json['error'] as Map?)?.cast<String, dynamic>() ?? const {};
      throw ApiException(res.statusCode, err['code'] as String? ?? 'error',
          err['message'] as String? ?? 'Request failed (${res.statusCode}).', json);
    }
    return json;
  }

  // ---- Memories ------------------------------------------------------------

  Future<({Memory memory, UploadTarget? upload})> createMemory(Map<String, dynamic> input) async {
    final j = await _req('POST', '/v1/memories', body: input);
    final up = j['upload'] as Map?;
    return (
      memory: Memory.fromJson(j['memory'] as Map<String, dynamic>),
      upload: up == null ? null : UploadTarget(up['url'] as String, up['path'] as String),
    );
  }

  Future<UploadTarget> uploadUrl(String id) async {
    final j = await _req('POST', '/v1/memories/$id/upload-url');
    return UploadTarget(j['url'] as String, j['path'] as String);
  }

  /// Streams the file straight to private storage with a one-time signed URL.
  Future<void> putFile(UploadTarget target, File file, String mime) async {
    final req = http.StreamedRequest('PUT', Uri.parse(target.url))
      ..headers['Content-Type'] = mime
      ..headers['x-upsert'] = 'true'
      ..contentLength = await file.length();
    unawaited(file.openRead().pipe(req.sink));
    try {
      final res = await _http.send(req).timeout(const Duration(minutes: 10));
      await res.stream.drain<void>();
      if (res.statusCode >= 300) {
        throw ApiException(res.statusCode, 'upload_failed', 'Upload failed (${res.statusCode}).');
      }
    } on SocketException {
      throw ApiException(0, 'network', 'Upload interrupted: you appear to be offline.');
    } on TimeoutException {
      throw ApiException(0, 'network', 'Upload timed out.');
    } on http.ClientException {
      throw ApiException(0, 'network', 'Upload interrupted.');
    }
  }

  Future<Memory> uploadComplete(String id) async =>
      Memory.fromJson((await _req('POST', '/v1/memories/$id/upload-complete'))['memory'] as Map<String, dynamic>);

  Future<({List<Memory> memories, String next})> listMemories({String? before, int limit = 30}) async {
    final q = <String, String>{'limit': '$limit', if (before != null && before.isNotEmpty) 'before': before};
    final j = await _req('GET', '/v1/memories?${Uri(queryParameters: q).query}');
    return (
      memories: ((j['memories'] as List?) ?? const []).map((e) => Memory.fromJson(e as Map<String, dynamic>)).toList(),
      next: j['next_cursor'] as String? ?? '',
    );
  }

  Future<Memory> getMemory(String id) async =>
      Memory.fromJson((await _req('GET', '/v1/memories/$id'))['memory'] as Map<String, dynamic>);

  Future<Memory> updateMemory(String id, Map<String, dynamic> patch) async =>
      Memory.fromJson((await _req('PATCH', '/v1/memories/$id', body: patch))['memory'] as Map<String, dynamic>);

  Future<void> deleteMemory(String id) => _req('DELETE', '/v1/memories/$id');

  Future<void> retryMemory(String id) => _req('POST', '/v1/memories/$id/retry');

  Future<List<Memory>> related(String id) async {
    final j = await _req('GET', '/v1/memories/$id/related');
    return ((j['related'] as List?) ?? const [])
        .map((e) => Memory.fromJson((e as Map)['memory'] as Map<String, dynamic>))
        .toList();
  }

  Future<String> downloadUrl(String id) async => (await _req('GET', '/v1/memories/$id/download'))['url'] as String;

  Future<String> createShare(String id, int hours) async =>
      (await _req('POST', '/v1/memories/$id/shares', body: {'expires_in_hours': hours}))['url'] as String;

  Future<List<Map<String, dynamic>>> listShares(String id) async =>
      ((await _req('GET', '/v1/memories/$id/shares'))['shares'] as List).cast<Map<String, dynamic>>();

  Future<void> revokeShare(String shareId) => _req('DELETE', '/v1/shares/$shareId');

  Future<List<({String label, List<Memory> memories})>> timeline({String? before}) async {
    final j = await _req('GET', '/v1/timeline${before == null ? '' : '?before=${Uri.encodeQueryComponent(before)}'}');
    return ((j['groups'] as List?) ?? const []).map((g) {
      final m = g as Map<String, dynamic>;
      return (
        label: m['label'] as String,
        memories: (m['memories'] as List).map((e) => Memory.fromJson(e as Map<String, dynamic>)).toList(),
      );
    }).toList();
  }

  Future<List<String>> categories() async {
    final j = await _req('GET', '/v1/facets');
    return ((j['categories'] as List?) ?? const []).map((e) => (e as Map)['name'] as String).toList();
  }

  // ---- Search & Ask --------------------------------------------------------

  Future<List<SearchHit>> search(String query, {List<String>? types, String? category, DateTime? from}) async {
    final j = await _req('POST', '/v1/search', body: {
      'query': query,
      if (types != null && types.isNotEmpty) 'types': types,
      if (category != null && category.isNotEmpty) 'category': category,
      if (from != null) 'from': from.toUtc().toIso8601String(),
    });
    return ((j['results'] as List?) ?? const []).map((e) => SearchHit.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<AskResult> ask(String question, {String? conversationId, List<String>? memoryIds}) async =>
      AskResult.fromJson(await _req('POST', '/v1/ask', body: {
        'question': question,
        'conversation_id': ?conversationId,
        if (memoryIds != null && memoryIds.isNotEmpty) 'memory_ids': memoryIds,
      }));

  // ---- Account -------------------------------------------------------------

  Future<Map<String, dynamic>> me() => _req('GET', '/v1/me');

  Future<Map<String, dynamic>> updateMe({String? name}) => _req('PATCH', '/v1/me', body: {'name': ?name});

  Future<void> deleteMyData({required bool deleteAccount}) =>
      _req('DELETE', '/v1/me/data', body: {'confirm': 'DELETE MY DATA', 'delete_account': deleteAccount});
}
