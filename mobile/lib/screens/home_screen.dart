import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../services/app_state.dart';
import '../widgets/memory_card.dart';
import 'add_memory.dart';

class HomeScreen extends StatelessWidget {
  const HomeScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final app = AppState.instance;
    return Scaffold(
      appBar: AppBar(
        title: const Text('Memory', style: TextStyle(fontWeight: FontWeight.bold)),
        actions: const [SyncIndicator(), SizedBox(width: 8)],
      ),
      body: RefreshIndicator(
        onRefresh: () async {
          await app.recent.refresh();
          await app.queue.sync();
        },
        child: ListenableBuilder(
          listenable: Listenable.merge([app.recent, app.queue]),
          builder: (context, _) {
            final recent = app.recent;
            final queue = app.queue.items;
            return ListView(
              padding: const EdgeInsets.fromLTRB(16, 8, 16, 96),
              children: [
                _SearchField(onSubmit: (q) => context.push('/search?q=${Uri.encodeQueryComponent(q)}')),
                const SizedBox(height: 12),
                FilledButton.icon(
                  style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(52)),
                  onPressed: () => showAddMemorySheet(context),
                  icon: const Icon(Icons.add),
                  label: const Text('Add Memory', style: TextStyle(fontSize: 16)),
                ),
                if (queue.isNotEmpty) ...[
                  const SizedBox(height: 20),
                  _SectionTitle('${queue.length} ${queue.length == 1 ? 'memory' : 'memories'} waiting to sync'),
                  for (final item in queue)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 8),
                      child: QueueCard(
                        item: item,
                        onDuplicate: (c) => app.queue.resolveDuplicate(item, c),
                        onRetry: () => app.queue.retry(item),
                        onDiscard: () => app.queue.remove(item),
                      ),
                    ),
                ],
                const SizedBox(height: 20),
                const _SectionTitle('Recent'),
                if (recent.fromCache && recent.error != null)
                  Padding(
                    padding: const EdgeInsets.only(bottom: 8),
                    child: Text('Offline — showing saved copy.', style: Theme.of(context).textTheme.bodySmall),
                  ),
                if (recent.loading) const Padding(padding: EdgeInsets.all(32), child: Center(child: CircularProgressIndicator())),
                if (!recent.loading && recent.items.isEmpty && recent.error != null && !recent.fromCache)
                  _ErrorBox(message: recent.error!, onRetry: recent.refresh),
                if (!recent.loading && recent.items.isEmpty && recent.error == null && queue.isEmpty) const _EmptyState(),
                for (final m in recent.items)
                  Padding(padding: const EdgeInsets.only(bottom: 8), child: MemoryCard(memory: m)),
                if (recent.next.isNotEmpty)
                  TextButton(onPressed: recent.loadMore, child: const Text('Load more')),
              ],
            );
          },
        ),
      ),
    );
  }
}

class SyncIndicator extends StatelessWidget {
  const SyncIndicator({super.key});

  @override
  Widget build(BuildContext context) {
    final q = AppState.instance.queue;
    return ListenableBuilder(
      listenable: q,
      builder: (context, _) {
        final scheme = Theme.of(context).colorScheme;
        if (!q.online) {
          return Chip(avatar: const Icon(Icons.cloud_off, size: 16), label: Text(q.pendingCount > 0 ? 'Offline · ${q.pendingCount}' : 'Offline'));
        }
        if (q.pendingCount > 0) {
          return Chip(avatar: const Icon(Icons.cloud_upload_outlined, size: 16), label: Text('Waiting to sync · ${q.pendingCount}'));
        }
        return Chip(
          avatar: Icon(Icons.check_circle, size: 16, color: scheme.primary),
          label: const Text('Synced'),
        );
      },
    );
  }
}

class _SearchField extends StatelessWidget {
  const _SearchField({required this.onSubmit});
  final ValueChanged<String> onSubmit;

  @override
  Widget build(BuildContext context) {
    return TextField(
      textInputAction: TextInputAction.search,
      decoration: const InputDecoration(prefixIcon: Icon(Icons.search), hintText: 'Ask your Memory…'),
      onSubmitted: (q) {
        if (q.trim().isNotEmpty) onSubmit(q.trim());
      },
    );
  }
}

class _SectionTitle extends StatelessWidget {
  const _SectionTitle(this.text);
  final String text;

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Text(text.toUpperCase(),
            style: Theme.of(context).textTheme.labelMedium?.copyWith(
                letterSpacing: 1, color: Theme.of(context).colorScheme.onSurfaceVariant, fontWeight: FontWeight.w600)),
      );
}

class _EmptyState extends StatelessWidget {
  const _EmptyState();

  @override
  Widget build(BuildContext context) => const Card(
        margin: EdgeInsets.zero,
        child: Padding(
          padding: EdgeInsets.all(24),
          child: Column(children: [
            Text('Your Memory is empty', style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600)),
            SizedBox(height: 6),
            Text('Save a screenshot, a receipt, a note or a voice memo — then just ask for it later.', textAlign: TextAlign.center),
          ]),
        ),
      );
}

class _ErrorBox extends StatelessWidget {
  const _ErrorBox({required this.message, required this.onRetry});
  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) => Card(
        margin: EdgeInsets.zero,
        child: ListTile(
          leading: Icon(Icons.error_outline, color: Theme.of(context).colorScheme.error),
          title: Text(message),
          trailing: TextButton(onPressed: onRetry, child: const Text('Retry')),
        ),
      );
}
