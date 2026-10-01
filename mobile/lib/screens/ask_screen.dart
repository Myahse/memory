import 'package:flutter/material.dart';

import '../models/memory.dart';
import '../services/api.dart';
import '../services/app_state.dart';
import '../widgets/memory_card.dart';
import 'search_screen.dart';

class _Turn {
  _Turn.user(this.text)
      : isUser = true,
        sources = const [];
  _Turn.assistant(this.text, this.sources) : isUser = false;
  final bool isUser;
  final String text;
  final List<Memory> sources;
}

const _examples = [
  'What laptop was I looking at last month?',
  'Find the receipt from Carrefour.',
  'What did I save about my university application?',
  'When did I write down this idea?',
];

class AskScreen extends StatefulWidget {
  const AskScreen({super.key, this.seed});
  final AskSeed? seed;

  @override
  State<AskScreen> createState() => _AskScreenState();
}

class _AskScreenState extends State<AskScreen> {
  final _input = TextEditingController();
  final _scroll = ScrollController();
  final List<_Turn> _turns = [];
  String? _conversationId;
  List<String>? _scope;
  bool _busy = false;

  @override
  void initState() {
    super.initState();
    if (widget.seed != null) {
      _scope = widget.seed!.memoryIds;
      WidgetsBinding.instance.addPostFrameCallback((_) => _ask(widget.seed!.question));
    }
  }

  Future<void> _ask(String q) async {
    q = q.trim();
    if (q.isEmpty || _busy) return;
    _input.clear();
    setState(() {
      _turns.add(_Turn.user(q));
      _busy = true;
    });
    _scrollDown();
    try {
      final res = await AppState.instance.api.ask(q, conversationId: _conversationId, memoryIds: _scope);
      _scope = null;
      _conversationId = res.conversationId;
      setState(() => _turns.add(_Turn.assistant(res.answer, res.sources)));
    } on ApiException catch (e) {
      setState(() => _turns.add(_Turn.assistant('⚠️ ${e.message}', const [])));
    } finally {
      if (mounted) setState(() => _busy = false);
      _scrollDown();
    }
  }

  void _scrollDown() => WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_scroll.hasClients) {
          _scroll.animateTo(_scroll.position.maxScrollExtent, duration: const Duration(milliseconds: 250), curve: Curves.easeOut);
        }
      });

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Scaffold(
      appBar: AppBar(
        title: const Text('Ask Memory'),
        actions: [
          IconButton(
            tooltip: 'New conversation',
            icon: const Icon(Icons.edit_square),
            onPressed: () => setState(() {
              _turns.clear();
              _conversationId = null;
            }),
          ),
        ],
      ),
      body: Column(children: [
        Expanded(
          child: ListView(controller: _scroll, padding: const EdgeInsets.all(16), children: [
            if (_turns.isEmpty) ...[
              Text('Ask anything about what you saved. Answers only use your own memories, with sources.',
                  style: TextStyle(color: scheme.onSurfaceVariant)),
              const SizedBox(height: 12),
              for (final e in _examples)
                Card(margin: const EdgeInsets.only(bottom: 8), child: ListTile(title: Text(e), onTap: () => _ask(e))),
            ],
            for (final t in _turns)
              t.isUser
                  ? Align(
                      alignment: Alignment.centerRight,
                      child: Container(
                        margin: const EdgeInsets.only(bottom: 12, left: 48),
                        padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
                        decoration: BoxDecoration(color: scheme.primary, borderRadius: BorderRadius.circular(18)),
                        child: Text(t.text, style: TextStyle(color: scheme.onPrimary)),
                      ),
                    )
                  : Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
                      Container(
                        margin: const EdgeInsets.only(bottom: 8, right: 32),
                        padding: const EdgeInsets.all(14),
                        decoration: BoxDecoration(color: scheme.surfaceContainerHigh, borderRadius: BorderRadius.circular(18)),
                        child: Text(t.text),
                      ),
                      if (t.sources.isNotEmpty) ...[
                        Text('Based on ${t.sources.length} ${t.sources.length == 1 ? 'memory' : 'memories'}',
                            style: Theme.of(context).textTheme.labelMedium?.copyWith(color: scheme.onSurfaceVariant)),
                        const SizedBox(height: 6),
                        for (final s in t.sources)
                          Padding(padding: const EdgeInsets.only(bottom: 6), child: MemoryCard(memory: s, compact: true)),
                      ],
                      const SizedBox(height: 12),
                    ]),
            if (_busy)
              Padding(
                padding: const EdgeInsets.all(8),
                child: Text('Searching your memories…', style: TextStyle(color: scheme.onSurfaceVariant)),
              ),
          ]),
        ),
        SafeArea(
          top: false,
          child: Padding(
            padding: const EdgeInsets.fromLTRB(12, 4, 12, 8),
            child: Row(children: [
              Expanded(
                child: TextField(
                  controller: _input,
                  textInputAction: TextInputAction.send,
                  onSubmitted: _ask,
                  decoration: const InputDecoration(hintText: 'Ask your Memory…'),
                ),
              ),
              const SizedBox(width: 8),
              IconButton.filled(onPressed: _busy ? null : () => _ask(_input.text), icon: const Icon(Icons.send)),
            ]),
          ),
        ),
      ]),
    );
  }
}
