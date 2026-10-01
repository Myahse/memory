import 'package:flutter/material.dart';

import '../models/memory.dart';
import '../services/api.dart';
import '../services/app_state.dart';
import '../widgets/memory_card.dart';

class TimelineScreen extends StatefulWidget {
  const TimelineScreen({super.key});
  @override
  State<TimelineScreen> createState() => _TimelineScreenState();
}

class _TimelineScreenState extends State<TimelineScreen> {
  final List<({String label, List<Memory> memories})> _groups = [];
  bool _loading = true;
  bool _more = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load(reset: true);
  }

  Future<void> _load({bool reset = false}) async {
    setState(() => _loading = true);
    try {
      String? before;
      if (!reset && _groups.isNotEmpty) before = _groups.last.memories.last.createdAt.toUtc().toIso8601String();
      final groups = await AppState.instance.api.timeline(before: before);
      setState(() {
        if (reset) _groups.clear();
        for (final g in groups) {
          if (_groups.isNotEmpty && _groups.last.label == g.label) {
            _groups.last.memories.addAll(g.memories);
          } else {
            _groups.add((label: g.label, memories: [...g.memories]));
          }
        }
        _more = groups.fold<int>(0, (n, g) => n + g.memories.length) >= 60;
        _error = null;
      });
    } on ApiException catch (e) {
      setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final t = Theme.of(context).textTheme;
    return Scaffold(
      appBar: AppBar(title: const Text('Timeline')),
      body: RefreshIndicator(
        onRefresh: () => _load(reset: true),
        child: ListView(padding: const EdgeInsets.all(16), children: [
          if (_error != null) Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
          if (!_loading && _groups.isEmpty && _error == null) const Text('Nothing saved yet.'),
          for (final g in _groups) ...[
            Padding(
              padding: const EdgeInsets.only(top: 8, bottom: 8),
              child: Text(g.label, style: t.titleMedium?.copyWith(fontWeight: FontWeight.w700)),
            ),
            for (final m in g.memories) Padding(padding: const EdgeInsets.only(bottom: 8), child: MemoryCard(memory: m, compact: true)),
          ],
          if (_loading) const Padding(padding: EdgeInsets.all(24), child: Center(child: CircularProgressIndicator())),
          if (!_loading && _more && _groups.isNotEmpty) TextButton(onPressed: _load, child: const Text('Older memories')),
        ]),
      ),
    );
  }
}
