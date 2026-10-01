import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:go_router/go_router.dart';
import 'package:intl/intl.dart';
import 'package:share_plus/share_plus.dart';
import 'package:url_launcher/url_launcher.dart';

import '../models/memory.dart';
import '../services/api.dart';
import '../services/app_state.dart';
import '../widgets/memory_card.dart';

class MemoryDetailScreen extends StatefulWidget {
  const MemoryDetailScreen({super.key, required this.id});
  final String id;

  @override
  State<MemoryDetailScreen> createState() => _MemoryDetailScreenState();
}

class _MemoryDetailScreenState extends State<MemoryDetailScreen> {
  final _api = AppState.instance.api;
  Memory? _memory;
  List<Memory> _related = [];
  String? _error;
  Timer? _poll;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _load() async {
    try {
      final m = await _api.getMemory(widget.id);
      if (!mounted) return;
      setState(() {
        _memory = m;
        _error = null;
      });
      _poll?.cancel();
      if (m.isProcessing) _poll = Timer(const Duration(seconds: 3), _load);
      if (m.status == 'ready') {
        final rel = await _api.related(widget.id);
        if (mounted) setState(() => _related = rel);
      }
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.status == 404 ? "This memory doesn't exist or was deleted." : e.message);
    }
  }

  Future<void> _delete() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('Delete this memory?'),
        content: const Text('The memory and its original file will be permanently deleted.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(c, false), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(c, true), child: const Text('Delete')),
        ],
      ),
    );
    if (ok != true) return;
    try {
      await _api.deleteMemory(widget.id);
      AppState.instance.recent.remove(widget.id);
      AppState.instance.toast('Memory deleted');
      if (mounted) context.pop();
    } on ApiException catch (e) {
      AppState.instance.toast(e.message);
    }
  }

  Future<void> _retry() async {
    try {
      await _api.retryMemory(widget.id);
      await _load();
    } on ApiException catch (e) {
      AppState.instance.toast(e.message);
    }
  }

  Future<void> _download() async {
    try {
      final url = await _api.downloadUrl(widget.id);
      await launchUrl(Uri.parse(url), mode: LaunchMode.externalApplication);
    } on ApiException catch (e) {
      AppState.instance.toast(e.message);
    }
  }

  @override
  Widget build(BuildContext context) {
    final m = _memory;
    if (_error != null) {
      return Scaffold(appBar: AppBar(), body: Center(child: Padding(padding: const EdgeInsets.all(24), child: Text(_error!))));
    }
    if (m == null) return Scaffold(appBar: AppBar(), body: const Center(child: CircularProgressIndicator()));
    final t = Theme.of(context).textTheme;
    final scheme = Theme.of(context).colorScheme;
    final entities = (m.metadata['entities'] as Map?)?.cast<String, dynamic>() ?? const {};
    final dup = m.metadata['possible_duplicate_of'] as String?;

    return Scaffold(
      appBar: AppBar(actions: [
        IconButton(tooltip: 'Edit', icon: const Icon(Icons.edit_outlined), onPressed: () => _edit(m)),
        IconButton(tooltip: 'Share', icon: const Icon(Icons.ios_share), onPressed: () => _share(m)),
        if (m.hasFile) IconButton(tooltip: 'Download', icon: const Icon(Icons.download_outlined), onPressed: _download),
        IconButton(tooltip: 'Delete', icon: const Icon(Icons.delete_outline), onPressed: _delete),
      ]),
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(padding: const EdgeInsets.all(16), children: [
          if (m.fileUrl != null && ['photo', 'screenshot', 'receipt'].contains(m.type))
            ClipRRect(
              borderRadius: BorderRadius.circular(16),
              child: InteractiveViewer(child: Image.network(m.fileUrl!, fit: BoxFit.contain)),
            )
          else if (m.thumbnailUrl != null)
            ClipRRect(borderRadius: BorderRadius.circular(16), child: Image.network(m.thumbnailUrl!, height: 180, fit: BoxFit.cover)),
          const SizedBox(height: 16),
          Text(
            '${MemoryType.emoji(m.type)} ${MemoryType.label(m.type)} · ${DateFormat.yMMMMd().add_jm().format((m.capturedAt ?? m.createdAt).toLocal())}'
            '${m.category.isNotEmpty ? ' · ${m.category}' : ''}',
            style: t.bodySmall?.copyWith(color: scheme.onSurfaceVariant),
          ),
          const SizedBox(height: 4),
          Text(m.displayTitle, style: t.headlineSmall?.copyWith(fontWeight: FontWeight.bold)),
          if (m.isProcessing)
            const Padding(
              padding: EdgeInsets.only(top: 12),
              child: Row(children: [
                SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2)),
                SizedBox(width: 8),
                Text('Processing memory…'),
              ]),
            ),
          if (m.isFailed)
            Card(
              color: scheme.errorContainer,
              margin: const EdgeInsets.only(top: 12),
              child: ListTile(
                title: Text(m.processingError ?? 'Processing failed', style: TextStyle(color: scheme.onErrorContainer)),
                trailing: FilledButton(onPressed: _retry, child: const Text('Retry')),
              ),
            ),
          if (dup != null)
            Card(
              margin: const EdgeInsets.only(top: 12),
              child: ListTile(
                leading: const Icon(Icons.content_copy),
                title: const Text('This looks similar to an existing memory.'),
                trailing: const Icon(Icons.chevron_right),
                onTap: () => context.push('/memory/$dup'),
              ),
            ),
          if (m.summary.isNotEmpty) _Section('Summary', Text(m.summary)),
          if (m.tags.isNotEmpty)
            _Section(
              'Tags',
              Wrap(spacing: 8, runSpacing: 4, children: [
                for (final tag in m.tags)
                  ActionChip(label: Text('#$tag'), onPressed: () => context.push('/search?q=${Uri.encodeQueryComponent(tag)}')),
              ]),
            ),
          if (entities.values.any((v) => v is List && v.isNotEmpty))
            _Section(
              'Details',
              Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                for (final e in entities.entries)
                  if (e.value is List && (e.value as List).isNotEmpty)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 4),
                      child: Text('${e.key[0].toUpperCase()}${e.key.substring(1)}: ${(e.value as List).join(', ')}'),
                    ),
              ]),
            ),
          if ((m.sourceUrl ?? '').isNotEmpty)
            _Section(
              'Link',
              InkWell(
                onTap: () => launchUrl(Uri.parse(m.sourceUrl!), mode: LaunchMode.externalApplication),
                child: Text(m.sourceUrl!, style: TextStyle(color: scheme.primary, decoration: TextDecoration.underline)),
              ),
            ),
          if ((m.content ?? '').isNotEmpty)
            _Section(m.type == 'voice' ? 'Transcript' : (m.type == 'note' ? 'Note' : 'Extracted text'), SelectableText(m.content!)),
          if (m.hasFile)
            Padding(
              padding: const EdgeInsets.only(top: 12),
              child: Text('Original file · ${m.mimeType ?? ''} · ${(m.fileSize / 1024).toStringAsFixed(0)} KB', style: t.bodySmall),
            ),
          if (_related.isNotEmpty) ...[
            const SizedBox(height: 24),
            Text('RELATED MEMORIES', style: t.labelMedium?.copyWith(letterSpacing: 1, color: scheme.onSurfaceVariant)),
            const SizedBox(height: 8),
            for (final r in _related) Padding(padding: const EdgeInsets.only(bottom: 8), child: MemoryCard(memory: r, compact: true)),
          ],
        ]),
      ),
    );
  }

  Future<void> _edit(Memory m) async {
    final title = TextEditingController(text: m.title);
    final summary = TextEditingController(text: m.summary);
    final category = TextEditingController(text: m.category);
    final tags = TextEditingController(text: m.tags.join(', '));
    final content = TextEditingController(text: m.content ?? '');
    final saved = await showModalBottomSheet<bool>(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (c) => Padding(
        padding: EdgeInsets.fromLTRB(16, 0, 16, MediaQuery.of(c).viewInsets.bottom + 16),
        child: SingleChildScrollView(
          child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.stretch, children: [
            const Text('Edit memory', style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600)),
            const SizedBox(height: 12),
            TextField(controller: title, decoration: const InputDecoration(labelText: 'Title')),
            const SizedBox(height: 8),
            TextField(controller: summary, maxLines: 3, decoration: const InputDecoration(labelText: 'Summary')),
            const SizedBox(height: 8),
            TextField(controller: category, decoration: const InputDecoration(labelText: 'Category')),
            const SizedBox(height: 8),
            TextField(controller: tags, decoration: const InputDecoration(labelText: 'Tags (comma separated)')),
            if (m.type == 'note') ...[
              const SizedBox(height: 8),
              TextField(controller: content, maxLines: 6, decoration: const InputDecoration(labelText: 'Note')),
            ],
            const SizedBox(height: 16),
            FilledButton(onPressed: () => Navigator.pop(c, true), child: const Text('Save')),
          ]),
        ),
      ),
    );
    if (saved != true) return;
    try {
      final updated = await _api.updateMemory(m.id, {
        'title': title.text.trim(),
        'summary': summary.text.trim(),
        'category': category.text.trim(),
        'tags': tags.text.split(',').map((s) => s.trim()).where((s) => s.isNotEmpty).toList(),
        if (m.type == 'note') 'content': content.text,
      });
      setState(() => _memory = updated);
      unawaited(AppState.instance.recent.refresh());
      AppState.instance.toast('Saved');
    } on ApiException catch (e) {
      AppState.instance.toast(e.message);
    }
  }

  Future<void> _share(Memory m) async {
    final hours = await showModalBottomSheet<int>(
      context: context,
      showDragHandle: true,
      builder: (c) => SafeArea(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          const Padding(
            padding: EdgeInsets.symmetric(horizontal: 16),
            child: Text('Share a temporary link to this memory only. The rest of your archive stays private. You can revoke it from the web app or here.'),
          ),
          for (final (label, h) in [('1 hour', 1), ('1 day', 24), ('7 days', 168), ('30 days', 720)])
            ListTile(title: Text('Link valid for $label'), onTap: () => Navigator.pop(c, h)),
          ListTile(
            leading: const Icon(Icons.link_off),
            title: const Text('Revoke existing links'),
            onTap: () => Navigator.pop(c, -1),
          ),
        ]),
      ),
    );
    if (hours == null) return;
    try {
      if (hours == -1) {
        final shares = await _api.listShares(m.id);
        for (final s in shares) {
          await _api.revokeShare(s['id'] as String);
        }
        AppState.instance.toast(shares.isEmpty ? 'No active links' : 'Revoked ${shares.length} link(s)');
        return;
      }
      final url = await _api.createShare(m.id, hours);
      await Clipboard.setData(ClipboardData(text: url));
      await SharePlus.instance.share(ShareParams(text: url, subject: m.displayTitle));
    } on ApiException catch (e) {
      AppState.instance.toast(e.message);
    }
  }
}

class _Section extends StatelessWidget {
  const _Section(this.title, this.child);
  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) => Card(
        margin: const EdgeInsets.only(top: 12),
        child: Padding(
          padding: const EdgeInsets.all(14),
          child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Text(title.toUpperCase(),
                style: Theme.of(context).textTheme.labelSmall?.copyWith(letterSpacing: 1, color: Theme.of(context).colorScheme.onSurfaceVariant)),
            const SizedBox(height: 6),
            child,
          ]),
        ),
      );
}
