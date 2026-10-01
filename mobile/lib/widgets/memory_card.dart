import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:intl/intl.dart';

import '../models/memory.dart';
import '../services/sync_queue.dart';

String timeAgo(DateTime t) {
  final d = DateTime.now().difference(t);
  if (d.inSeconds < 45) return 'just now';
  if (d.inMinutes < 60) return '${d.inMinutes} min ago';
  final now = DateTime.now();
  final local = t.toLocal();
  if (local.year == now.year && local.month == now.month && local.day == now.day) {
    return d.inHours <= 1 ? '1 hour ago' : '${d.inHours} hours ago';
  }
  final y = now.subtract(const Duration(days: 1));
  if (local.year == y.year && local.month == y.month && local.day == y.day) return 'Yesterday';
  return DateFormat(local.year == now.year ? 'MMMM d' : 'MMMM d, y').format(local);
}

class MemoryCard extends StatelessWidget {
  const MemoryCard({super.key, required this.memory, this.snippet, this.compact = false});
  final Memory memory;
  final String? snippet;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final sub = snippet?.isNotEmpty == true ? snippet! : memory.summary;
    return Card(
      margin: EdgeInsets.zero,
      child: InkWell(
        borderRadius: BorderRadius.circular(16),
        onTap: () => context.push('/memory/${memory.id}'),
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _Thumb(memory: memory),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text('${MemoryType.emoji(memory.type)} ${MemoryType.label(memory.type)} · ${timeAgo(memory.createdAt)}',
                        style: Theme.of(context).textTheme.labelSmall?.copyWith(color: scheme.onSurfaceVariant)),
                    const SizedBox(height: 2),
                    Text(memory.displayTitle,
                        maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w600)),
                    if (!compact && sub.isNotEmpty)
                      Text(sub,
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: Theme.of(context).textTheme.bodySmall?.copyWith(color: scheme.onSurfaceVariant)),
                    if (memory.isProcessing) const _StatusLine(icon: null, text: 'Processing memory…'),
                    if (memory.isFailed) const _StatusLine(icon: Icons.error_outline, text: 'Processing failed', error: true),
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _Thumb extends StatelessWidget {
  const _Thumb({required this.memory});
  final Memory memory;

  @override
  Widget build(BuildContext context) {
    final bg = Theme.of(context).colorScheme.surfaceContainerHighest;
    return ClipRRect(
      borderRadius: BorderRadius.circular(12),
      child: Container(
        width: 52,
        height: 52,
        color: bg,
        alignment: Alignment.center,
        child: memory.thumbnailUrl != null
            ? Image.network(memory.thumbnailUrl!, width: 52, height: 52, fit: BoxFit.cover,
                errorBuilder: (_, _, _) => Text(MemoryType.emoji(memory.type), style: const TextStyle(fontSize: 24)))
            : Text(MemoryType.emoji(memory.type), style: const TextStyle(fontSize: 24)),
      ),
    );
  }
}

class _StatusLine extends StatelessWidget {
  const _StatusLine({required this.icon, required this.text, this.error = false});
  final IconData? icon;
  final String text;
  final bool error;

  @override
  Widget build(BuildContext context) {
    final color = error ? Theme.of(context).colorScheme.error : Theme.of(context).colorScheme.primary;
    return Padding(
      padding: const EdgeInsets.only(top: 4),
      child: Row(children: [
        if (icon == null)
          SizedBox(width: 12, height: 12, child: CircularProgressIndicator(strokeWidth: 2, color: color))
        else
          Icon(icon, size: 14, color: color),
        const SizedBox(width: 6),
        Text(text, style: TextStyle(fontSize: 12, color: color, fontWeight: FontWeight.w500)),
      ]),
    );
  }
}

/// A capture still in the local offline queue.
class QueueCard extends StatelessWidget {
  const QueueCard({super.key, required this.item, required this.onDuplicate, required this.onRetry, required this.onDiscard});
  final QueueItem item;
  final void Function(String choice) onDuplicate;
  final VoidCallback onRetry;
  final VoidCallback onDiscard;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final (String status, Color color) = switch (item.state) {
      QueueState.waiting => ('Waiting to sync', scheme.onSurfaceVariant),
      QueueState.uploading => ('Uploading…', scheme.primary),
      QueueState.duplicate => ('This looks similar to an existing memory.', Colors.orange.shade700),
      QueueState.failed => (item.error ?? 'Upload failed', scheme.error),
    };
    return Card(
      margin: EdgeInsets.zero,
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(children: [
              Text(MemoryType.emoji(item.type), style: const TextStyle(fontSize: 22)),
              const SizedBox(width: 10),
              Expanded(
                child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  Text(item.displayTitle, maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontWeight: FontWeight.w600)),
                  Text(status, style: TextStyle(fontSize: 12, color: color)),
                ]),
              ),
              if (item.state == QueueState.uploading)
                const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
              else if (item.state == QueueState.waiting)
                Icon(Icons.cloud_upload_outlined, color: scheme.onSurfaceVariant, size: 20),
            ]),
            if (item.state == QueueState.duplicate)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Wrap(spacing: 8, children: [
                  TextButton(onPressed: () => onDuplicate('cancel'), child: const Text('Cancel')),
                  OutlinedButton(onPressed: () => onDuplicate('replace'), child: const Text('Replace')),
                  FilledButton(onPressed: () => onDuplicate('keep_both'), child: const Text('Keep both')),
                ]),
              ),
            if (item.state == QueueState.failed)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Wrap(spacing: 8, children: [
                  TextButton(onPressed: onDiscard, child: const Text('Discard')),
                  FilledButton.tonal(onPressed: onRetry, child: const Text('Retry')),
                ]),
              ),
          ],
        ),
      ),
    );
  }
}
