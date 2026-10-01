import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../models/memory.dart';
import '../services/api.dart';
import '../services/app_state.dart';
import '../widgets/memory_card.dart';

const _filters = <(String, List<String>)>[
  ('All', []),
  ('Documents', ['pdf', 'document']),
  ('Screenshots', ['screenshot']),
  ('Photos', ['photo']),
  ('Receipts', ['receipt']),
  ('Voice', ['voice']),
  ('Notes', ['note']),
  ('Links', ['link']),
];

const _ranges = <(String, int)>[('Any time', 0), ('Past week', 7), ('Past month', 31), ('Past year', 365)];

class SearchScreen extends StatefulWidget {
  const SearchScreen({super.key, this.initialQuery = ''});
  final String initialQuery;

  @override
  State<SearchScreen> createState() => _SearchScreenState();
}

class _SearchScreenState extends State<SearchScreen> {
  late final _controller = TextEditingController(text: widget.initialQuery);
  String _filter = 'All';
  int _days = 0;
  String? _category;
  List<String> _categories = [];
  List<SearchHit>? _results;
  bool _loading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    if (widget.initialQuery.isNotEmpty) _search();
    AppState.instance.api.categories().then((c) {
      if (mounted) setState(() => _categories = c);
    }).catchError((_) {});
  }

  Future<void> _search() async {
    final q = _controller.text.trim();
    if (q.isEmpty) return;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final types = _filters.firstWhere((f) => f.$1 == _filter).$2;
      final res = await AppState.instance.api.search(q,
          types: types,
          category: _category,
          from: _days == 0 ? null : DateTime.now().subtract(Duration(days: _days)));
      if (mounted) setState(() => _results = res);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final results = _results;
    return Scaffold(
      appBar: AppBar(
        titleSpacing: 0,
        title: TextField(
          controller: _controller,
          autofocus: widget.initialQuery.isEmpty,
          textInputAction: TextInputAction.search,
          decoration: const InputDecoration(hintText: 'Ask your Memory…', border: InputBorder.none, filled: false),
          onSubmitted: (_) => _search(),
        ),
        actions: [IconButton(icon: const Icon(Icons.search), onPressed: _search)],
      ),
      body: ListView(padding: const EdgeInsets.fromLTRB(16, 0, 16, 32), children: [
        SizedBox(
          height: 48,
          child: ListView(scrollDirection: Axis.horizontal, children: [
            for (final f in _filters)
              Padding(
                padding: const EdgeInsets.only(right: 8),
                child: ChoiceChip(
                  label: Text(f.$1),
                  selected: _filter == f.$1,
                  onSelected: (_) {
                    setState(() => _filter = f.$1);
                    _search();
                  },
                ),
              ),
          ]),
        ),
        Row(children: [
          DropdownButton<int>(
            value: _days,
            items: [for (final r in _ranges) DropdownMenuItem(value: r.$2, child: Text(r.$1))],
            onChanged: (v) {
              setState(() => _days = v ?? 0);
              _search();
            },
          ),
          const SizedBox(width: 16),
          if (_categories.isNotEmpty)
            DropdownButton<String?>(
              value: _category,
              hint: const Text('All categories'),
              items: [
                const DropdownMenuItem(value: null, child: Text('All categories')),
                for (final c in _categories) DropdownMenuItem(value: c, child: Text(c)),
              ],
              onChanged: (v) {
                setState(() => _category = v);
                _search();
              },
            ),
        ]),
        if (_loading) const Padding(padding: EdgeInsets.all(32), child: Center(child: CircularProgressIndicator())),
        if (_error != null) Padding(padding: const EdgeInsets.all(16), child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error))),
        if (results != null && !_loading) ...[
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Text(results.isEmpty
                ? 'No matching memories.'
                : '${results.length} relevant ${results.length == 1 ? 'memory' : 'memories'}'),
          ),
          for (final h in results)
            Padding(padding: const EdgeInsets.only(bottom: 8), child: MemoryCard(memory: h.memory, snippet: h.snippet)),
          if (results.isNotEmpty)
            FilledButton.icon(
              onPressed: () => context.push('/ask', extra: AskSeed(_controller.text.trim(), results.take(8).map((h) => h.memory.id).toList())),
              icon: const Icon(Icons.arrow_forward),
              label: const Text('Ask Memory about these results'),
            ),
        ],
      ]),
    );
  }
}

/// Question + memory ids handed from search results to the chat.
class AskSeed {
  AskSeed(this.question, this.memoryIds);
  final String question;
  final List<String> memoryIds;
}
