import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:intl/intl.dart';
import 'package:supabase_flutter/supabase_flutter.dart';

import '../services/api.dart';
import '../services/app_state.dart';

String _bytes(num n) {
  if (n < 1024) return '$n B';
  const units = ['KB', 'MB', 'GB', 'TB'];
  var v = n / 1024;
  var i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return '${v < 10 ? v.toStringAsFixed(1) : v.round()} ${units[i]}';
}

class ProfileScreen extends StatefulWidget {
  const ProfileScreen({super.key});
  @override
  State<ProfileScreen> createState() => _ProfileScreenState();
}

class _ProfileScreenState extends State<ProfileScreen> {
  Map<String, dynamic>? _me;
  String? _error;
  final _name = TextEditingController();

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final me = await AppState.instance.api.me();
      setState(() {
        _me = me;
        _name.text = (me['profile'] as Map)['name'] as String? ?? '';
      });
    } on ApiException catch (e) {
      setState(() => _error = e.message);
    }
  }

  Future<void> _saveName() async {
    try {
      final me = await AppState.instance.api.updateMe(name: _name.text.trim());
      setState(() => _me = me);
      AppState.instance.toast('Profile saved');
    } on ApiException catch (e) {
      AppState.instance.toast(e.message);
    }
  }

  @override
  Widget build(BuildContext context) {
    final profile = (_me?['profile'] as Map?)?.cast<String, dynamic>();
    final usage = (_me?['usage'] as Map?)?.cast<String, dynamic>();
    final scheme = Theme.of(context).colorScheme;
    return Scaffold(
      appBar: AppBar(title: const Text('Profile'), actions: [
        IconButton(tooltip: 'Settings', icon: const Icon(Icons.settings_outlined), onPressed: () => context.push('/settings')),
      ]),
      body: RefreshIndicator(
        onRefresh: _load,
        child: ListView(padding: const EdgeInsets.all(16), children: [
          if (_error != null) Text(_error!, style: TextStyle(color: scheme.error)),
          if (profile == null && _error == null) const Center(child: CircularProgressIndicator()),
          if (profile != null) ...[
            Row(children: [
              CircleAvatar(
                radius: 32,
                backgroundImage: (profile['avatar_url'] as String? ?? '').isNotEmpty ? NetworkImage(profile['avatar_url'] as String) : null,
                child: (profile['avatar_url'] as String? ?? '').isEmpty
                    ? Text(((profile['name'] as String?)?.isNotEmpty == true ? profile['name'] as String : profile['email'] as String? ?? '?')
                        .substring(0, 1)
                        .toUpperCase())
                    : null,
              ),
              const SizedBox(width: 16),
              Expanded(
                child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  Text((profile['name'] as String?)?.isNotEmpty == true ? profile['name'] as String : 'Unnamed',
                      style: Theme.of(context).textTheme.titleLarge),
                  Text(profile['email'] as String? ?? '', style: TextStyle(color: scheme.onSurfaceVariant)),
                  Text('Member since ${DateFormat.yMMMMd().format(DateTime.parse(profile['created_at'] as String).toLocal())}',
                      style: Theme.of(context).textTheme.bodySmall),
                ]),
              ),
            ]),
            const SizedBox(height: 20),
            Row(children: [
              Expanded(child: TextField(controller: _name, decoration: const InputDecoration(labelText: 'Name'))),
              const SizedBox(width: 8),
              FilledButton(onPressed: _saveName, child: const Text('Save')),
            ]),
          ],
          if (usage != null) ...[
            const SizedBox(height: 24),
            Card(
              margin: EdgeInsets.zero,
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                  Row(children: [
                    Text('Plan', style: Theme.of(context).textTheme.titleMedium),
                    const Spacer(),
                    Chip(label: Text(usage['plan'] == 'pro' ? 'Pro' : 'Free')),
                  ]),
                  _Meter('Storage', usage['storage_used'] as num, usage['storage_limit'] as num, fmt: _bytes),
                  _Meter('AI questions this month', usage['ai_queries'] as num, usage['ai_query_limit'] as num),
                  _Meter('Items processed this month', usage['processed_items'] as num, usage['processed_limit'] as num),
                  const SizedBox(height: 8),
                  Text('${usage['memory_count']} memories saved'),
                ]),
              ),
            ),
          ],
        ]),
      ),
    );
  }
}

class _Meter extends StatelessWidget {
  const _Meter(this.label, this.used, this.limit, {this.fmt});
  final String label;
  final num used;
  final num limit;
  final String Function(num)? fmt;

  @override
  Widget build(BuildContext context) {
    final f = fmt ?? (n) => '$n';
    return Padding(
      padding: const EdgeInsets.only(top: 12),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Row(children: [Text(label), const Spacer(), Text(limit > 0 ? '${f(used)} / ${f(limit)}' : '${f(used)} / Unlimited')]),
        if (limit > 0) ...[
          const SizedBox(height: 4),
          LinearProgressIndicator(value: (used / limit).clamp(0, 1).toDouble(), borderRadius: BorderRadius.circular(4)),
        ],
      ]),
    );
  }
}

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final theme = AppState.instance.theme;
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(children: [
        const ListTile(title: Text('Appearance', style: TextStyle(fontWeight: FontWeight.w600))),
        ListenableBuilder(
          listenable: theme,
          builder: (context, _) => Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: SegmentedButton<ThemeMode>(
              segments: const [
                ButtonSegment(value: ThemeMode.light, label: Text('Light'), icon: Icon(Icons.light_mode_outlined)),
                ButtonSegment(value: ThemeMode.dark, label: Text('Dark'), icon: Icon(Icons.dark_mode_outlined)),
                ButtonSegment(value: ThemeMode.system, label: Text('System')),
              ],
              selected: {theme.mode},
              onSelectionChanged: (s) => theme.set(s.first),
            ),
          ),
        ),
        const Divider(height: 32),
        const ListTile(title: Text('Privacy', style: TextStyle(fontWeight: FontWeight.w600))),
        const ListTile(
          subtitle: Text('Your memories are private to your account. They are protected by row-level security, '
              'stored in a private bucket and only ever served through short-lived signed links.'),
        ),
        ListTile(
          leading: const Icon(Icons.logout),
          title: const Text('Sign out'),
          onTap: () async {
            await AppState.instance.queue.clear();
            await Supabase.instance.client.auth.signOut();
          },
        ),
        ListTile(
          leading: Icon(Icons.delete_forever, color: Theme.of(context).colorScheme.error),
          title: Text('Delete my data', style: TextStyle(color: Theme.of(context).colorScheme.error)),
          onTap: () => showDialog(context: context, builder: (_) => const _DeleteAllDialog()),
        ),
      ]),
    );
  }
}

class _DeleteAllDialog extends StatefulWidget {
  const _DeleteAllDialog();
  @override
  State<_DeleteAllDialog> createState() => _DeleteAllDialogState();
}

class _DeleteAllDialogState extends State<_DeleteAllDialog> {
  final _phrase = TextEditingController();
  bool _account = false;
  bool _busy = false;

  Future<void> _delete() async {
    setState(() => _busy = true);
    try {
      await AppState.instance.api.deleteMyData(deleteAccount: _account);
      await AppState.instance.queue.clear();
      await AppState.instance.recent.clear();
      AppState.instance.toast('All your data was permanently deleted.');
      if (_account) await Supabase.instance.client.auth.signOut();
      if (mounted) Navigator.pop(context);
    } on ApiException catch (e) {
      AppState.instance.toast(e.message);
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Delete my data'),
      content: SingleChildScrollView(
        child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
          const Text('This permanently deletes, with no way to recover:\n'
              '• all memories and metadata\n• all uploaded files\n• all embeddings and search indexes\n'
              '• all tags and conversations\n• all share links'),
          CheckboxListTile(
            contentPadding: EdgeInsets.zero,
            value: _account,
            onChanged: (v) => setState(() => _account = v ?? false),
            title: const Text('Also delete my account'),
          ),
          const Text('Type DELETE MY DATA to confirm'),
          TextField(controller: _phrase, onChanged: (_) => setState(() {})),
        ]),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Cancel')),
        FilledButton(
          style: FilledButton.styleFrom(backgroundColor: Theme.of(context).colorScheme.error),
          onPressed: _phrase.text == 'DELETE MY DATA' && !_busy ? _delete : null,
          child: Text(_busy ? 'Deleting…' : 'Delete everything'),
        ),
      ],
    );
  }
}
