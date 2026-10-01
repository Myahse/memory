class MemoryType {
  static const all = ['photo', 'screenshot', 'pdf', 'document', 'receipt', 'voice', 'note', 'link'];

  static String emoji(String t) => const {
        'photo': '📸',
        'screenshot': '📱',
        'pdf': '📄',
        'document': '📄',
        'receipt': '🧾',
        'voice': '🎙️',
        'note': '📝',
        'link': '🔗',
      }[t] ??
      '🗂️';

  static String label(String t) => const {
        'photo': 'Photo',
        'screenshot': 'Screenshot',
        'pdf': 'PDF',
        'document': 'Document',
        'receipt': 'Receipt',
        'voice': 'Voice note',
        'note': 'Note',
        'link': 'Link',
      }[t] ??
      t;
}

class Memory {
  Memory({
    required this.id,
    required this.type,
    required this.status,
    required this.title,
    required this.summary,
    required this.category,
    required this.tags,
    required this.createdAt,
    required this.metadata,
    this.content,
    this.sourceUrl,
    this.mimeType,
    this.fileSize = 0,
    this.capturedAt,
    this.processingError,
    this.hasFile = false,
    this.thumbnailUrl,
    this.fileUrl,
  });

  final String id;
  final String type;
  final String status;
  final String title;
  final String summary;
  final String category;
  final List<String> tags;
  final DateTime createdAt;
  final Map<String, dynamic> metadata;
  final String? content;
  final String? sourceUrl;
  final String? mimeType;
  final int fileSize;
  final DateTime? capturedAt;
  final String? processingError;
  final bool hasFile;
  final String? thumbnailUrl;
  final String? fileUrl;

  bool get isProcessing => status == 'pending' || status == 'processing';
  bool get isFailed => status == 'failed';

  String get displayTitle {
    if (title.isNotEmpty) return title;
    if (type == 'link' && (sourceUrl ?? '').isNotEmpty) return sourceUrl!;
    if (type == 'note' && (content ?? '').isNotEmpty) {
      return content!.length > 80 ? content!.substring(0, 80) : content!;
    }
    return MemoryType.label(type);
  }

  factory Memory.fromJson(Map<String, dynamic> j) => Memory(
        id: j['id'] as String,
        type: j['type'] as String,
        status: j['status'] as String? ?? 'ready',
        title: j['title'] as String? ?? '',
        summary: j['summary'] as String? ?? '',
        category: j['category'] as String? ?? '',
        tags: ((j['tags'] as List?) ?? const []).cast<String>(),
        createdAt: DateTime.parse(j['created_at'] as String),
        metadata: (j['metadata'] as Map?)?.cast<String, dynamic>() ?? const {},
        content: j['content'] as String?,
        sourceUrl: j['source_url'] as String?,
        mimeType: j['mime_type'] as String?,
        fileSize: (j['file_size'] as num?)?.toInt() ?? 0,
        capturedAt: j['captured_at'] == null ? null : DateTime.tryParse(j['captured_at'] as String),
        processingError: j['processing_error'] as String?,
        hasFile: j['has_file'] as bool? ?? false,
        thumbnailUrl: j['thumbnail_url'] as String?,
        fileUrl: j['file_url'] as String?,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'type': type,
        'status': status,
        'title': title,
        'summary': summary,
        'category': category,
        'tags': tags,
        'created_at': createdAt.toIso8601String(),
        'metadata': metadata,
        'content': content,
        'source_url': sourceUrl,
        'mime_type': mimeType,
        'file_size': fileSize,
        'captured_at': capturedAt?.toIso8601String(),
        'processing_error': processingError,
        'has_file': hasFile,
        // Signed URLs expire; don't cache them.
      };
}

class SearchHit {
  SearchHit(this.memory, this.snippet);
  final Memory memory;
  final String snippet;

  factory SearchHit.fromJson(Map<String, dynamic> j) =>
      SearchHit(Memory.fromJson(j['memory'] as Map<String, dynamic>), j['snippet'] as String? ?? '');
}

class AskResult {
  AskResult({required this.conversationId, required this.answer, required this.found, required this.sources});
  final String conversationId;
  final String answer;
  final bool found;
  final List<Memory> sources;

  factory AskResult.fromJson(Map<String, dynamic> j) => AskResult(
        conversationId: j['conversation_id'] as String,
        answer: j['answer'] as String,
        found: j['found'] as bool? ?? false,
        sources: ((j['sources'] as List?) ?? const []).map((e) => Memory.fromJson(e as Map<String, dynamic>)).toList(),
      );
}
