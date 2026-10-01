import 'package:flutter_test/flutter_test.dart';
import 'package:memory_app/models/memory.dart';
import 'package:memory_app/services/sync_queue.dart';

void main() {
  final json = {
    'id': 'abc',
    'type': 'note',
    'status': 'processing',
    'title': '',
    'summary': 'Laptop note',
    'category': 'Technology',
    'tags': ['laptop'],
    'created_at': '2026-10-01T12:00:00Z',
    'metadata': {'entities': {}},
    'content': 'My laptop model is HP Omen 16 Max.',
    'has_file': false,
  };

  test('Memory parses API JSON and derives a title', () {
    final m = Memory.fromJson(json);
    expect(m.isProcessing, isTrue);
    expect(m.displayTitle, 'My laptop model is HP Omen 16 Max.');
    expect(Memory.fromJson(m.toJson()).summary, 'Laptop note');
  });

  test('QueueItem round-trips and never persists the uploading state', () {
    final item = QueueItem(id: 'q1', type: 'voice', createdAt: DateTime.utc(2026), filePath: '/tmp/a.m4a', mime: 'audio/mp4', size: 10)
      ..state = QueueState.uploading
      ..duplicates = [Memory.fromJson(json)];
    final back = QueueItem.fromJson(item.toJson());
    expect(back.state, QueueState.waiting);
    expect(back.duplicates.single.id, 'abc');
    expect(back.displayTitle, 'Voice note');
  });

  test('type metadata', () {
    expect(MemoryType.emoji('receipt'), '🧾');
    expect(MemoryType.all, contains('link'));
  });
}
