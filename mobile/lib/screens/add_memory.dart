import 'dart:async';
import 'dart:io';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';
import 'package:mime/mime.dart';
import 'package:path_provider/path_provider.dart';
import 'package:record/record.dart';

import '../services/app_state.dart';

const _documentExtensions = ['pdf', 'docx', 'txt', 'md', 'csv', 'html', 'htm', 'rtf', 'json'];

/// Bottom sheet with every way to add a memory. No folders, no categories.
Future<void> showAddMemorySheet(BuildContext context) {
  return showModalBottomSheet(
    context: context,
    showDragHandle: true,
    builder: (sheet) {
      Widget option(IconData icon, String label, Future<void> Function() onTap) => ListTile(
            leading: Icon(icon, color: Theme.of(sheet).colorScheme.primary),
            title: Text(label),
            onTap: () async {
              Navigator.of(sheet).pop();
              await onTap();
            },
          );
      return SafeArea(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          const Padding(
            padding: EdgeInsets.only(bottom: 8),
            child: Text('Add Memory', style: TextStyle(fontSize: 18, fontWeight: FontWeight.w600)),
          ),
          option(Icons.photo_camera_outlined, 'Take Photo', () => _pickImage(context, ImageSource.camera)),
          option(Icons.photo_library_outlined, 'Choose Photo', () => _pickImage(context, ImageSource.gallery)),
          option(Icons.description_outlined, 'Upload Document', () => _pickDocument(context)),
          option(Icons.mic_none, 'Record Voice', () async => context.push('/add/voice')),
          option(Icons.edit_note, 'Write Note', () async => context.push('/add/note')),
          option(Icons.link, 'Save Link', () async => context.push('/add/link')),
        ]),
      );
    },
  );
}

/// The phone only compresses (max 2048px JPEG); all understanding happens in the cloud.
Future<void> _pickImage(BuildContext context, ImageSource source) async {
  final app = AppState.instance;
  try {
    final picker = ImagePicker();
    final files = source == ImageSource.camera
        ? [?(await picker.pickImage(source: source, maxWidth: 2048, maxHeight: 2048, imageQuality: 85))]
        : await picker.pickMultiImage(maxWidth: 2048, maxHeight: 2048, imageQuality: 85);
    for (final x in files) {
      final file = File(x.path);
      final head = await file.openRead(0, 16).first;
      final mime = lookupMimeType(x.path, headerBytes: head) ?? 'image/jpeg';
      final name = x.name.toLowerCase();
      final type = source == ImageSource.gallery && (name.contains('screenshot') || name.contains('screen_shot')) ? 'screenshot' : 'photo';
      await app.queue.addFile(file, type: type, mime: mime, metadata: {'original_name': x.name},
          capturedAt: source == ImageSource.camera ? DateTime.now() : await file.lastModified());
    }
    if (files.isNotEmpty) app.toast(files.length == 1 ? 'Saved — processing memory…' : 'Saved ${files.length} photos — processing…');
  } catch (e) {
    app.toast('Could not add photo: $e');
  }
}

Future<void> _pickDocument(BuildContext context) async {
  final app = AppState.instance;
  try {
    final files = await FilePicker.pickFiles(type: FileType.custom, allowedExtensions: _documentExtensions);
    for (final f in files) {
      if (f.path == null) continue;
      final mime = lookupMimeType(f.path!) ?? _mimeForExtension(f.extension);
      if (mime == null) {
        app.toast('Unsupported file: ${f.name}');
        continue;
      }
      final type = mime == 'application/pdf' ? 'pdf' : 'document';
      await app.queue.addFile(File(f.path!), type: type, mime: mime, metadata: {'original_name': f.name});
    }
    if (files.isNotEmpty) app.toast('Saved — processing memory…');
  } catch (e) {
    app.toast('Could not add document: $e');
  }
}

String? _mimeForExtension(String? ext) => switch (ext?.toLowerCase()) {
      'md' => 'text/markdown',
      'docx' => 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
      'rtf' => 'application/rtf',
      'txt' => 'text/plain',
      'csv' => 'text/csv',
      _ => null,
    };

class NoteScreen extends StatefulWidget {
  const NoteScreen({super.key});
  @override
  State<NoteScreen> createState() => _NoteScreenState();
}

class _NoteScreenState extends State<NoteScreen> {
  final _title = TextEditingController();
  final _body = TextEditingController();

  Future<void> _save() async {
    final text = _body.text.trim();
    if (text.isEmpty) return;
    await AppState.instance.queue.addNote(text, title: _title.text.trim().isEmpty ? null : _title.text.trim());
    AppState.instance.toast('Note saved');
    if (mounted) context.pop();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Write Note'), actions: [
        TextButton(onPressed: _save, child: const Text('Save')),
      ]),
      body: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(children: [
          TextField(controller: _title, decoration: const InputDecoration(hintText: 'Title (optional)')),
          const SizedBox(height: 12),
          Expanded(
            child: TextField(
              controller: _body,
              autofocus: true,
              maxLines: null,
              expands: true,
              textAlignVertical: TextAlignVertical.top,
              decoration: const InputDecoration(hintText: 'My laptop model is HP Omen 16 Max…'),
            ),
          ),
        ]),
      ),
    );
  }
}

class LinkScreen extends StatefulWidget {
  const LinkScreen({super.key});
  @override
  State<LinkScreen> createState() => _LinkScreenState();
}

class _LinkScreenState extends State<LinkScreen> {
  final _url = TextEditingController();
  final _note = TextEditingController();
  String? _error;

  Future<void> _save() async {
    var url = _url.text.trim();
    if (url.isEmpty) return;
    if (!RegExp(r'^https?://', caseSensitive: false).hasMatch(url)) url = 'https://$url';
    final uri = Uri.tryParse(url);
    if (uri == null || uri.host.isEmpty) {
      setState(() => _error = 'Please enter a valid link.');
      return;
    }
    await AppState.instance.queue.addLink(url, note: _note.text.trim().isEmpty ? null : _note.text.trim());
    AppState.instance.toast('Link saved');
    if (mounted) context.pop();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Save Link')),
      body: ListView(padding: const EdgeInsets.all(16), children: [
        TextField(
          controller: _url,
          autofocus: true,
          keyboardType: TextInputType.url,
          decoration: InputDecoration(hintText: 'https://…', errorText: _error),
        ),
        const SizedBox(height: 12),
        TextField(controller: _note, decoration: const InputDecoration(hintText: 'Why are you saving this? (optional)')),
        const SizedBox(height: 20),
        FilledButton(onPressed: _save, child: const Text('Save link')),
      ]),
    );
  }
}

class VoiceRecorderScreen extends StatefulWidget {
  const VoiceRecorderScreen({super.key});
  @override
  State<VoiceRecorderScreen> createState() => _VoiceRecorderScreenState();
}

class _VoiceRecorderScreenState extends State<VoiceRecorderScreen> {
  final _recorder = AudioRecorder();
  Timer? _timer;
  Duration _elapsed = Duration.zero;
  bool _recording = false;
  String? _path;
  String? _error;

  @override
  void dispose() {
    _timer?.cancel();
    _recorder.dispose();
    super.dispose();
  }

  Future<void> _start() async {
    if (!await _recorder.hasPermission()) {
      setState(() => _error = 'Microphone permission is needed to record voice notes.');
      return;
    }
    final dir = await getTemporaryDirectory();
    final path = '${dir.path}/voice-${DateTime.now().millisecondsSinceEpoch}.m4a';
    await _recorder.start(const RecordConfig(encoder: AudioEncoder.aacLc, bitRate: 64000, sampleRate: 22050, numChannels: 1), path: path);
    final started = DateTime.now();
    _timer = Timer.periodic(const Duration(milliseconds: 250), (_) => setState(() => _elapsed = DateTime.now().difference(started)));
    setState(() {
      _recording = true;
      _path = null;
      _error = null;
    });
  }

  Future<void> _stop() async {
    _timer?.cancel();
    final path = await _recorder.stop();
    setState(() {
      _recording = false;
      _path = path;
    });
  }

  Future<void> _save() async {
    if (_path == null) return;
    await AppState.instance.queue.addFile(File(_path!),
        type: 'voice', mime: 'audio/mp4', metadata: {'duration_ms': _elapsed.inMilliseconds}, capturedAt: DateTime.now());
    AppState.instance.toast('Voice note saved — transcribing…');
    if (mounted) context.pop();
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final mm = _elapsed.inMinutes.toString().padLeft(2, '0');
    final ss = (_elapsed.inSeconds % 60).toString().padLeft(2, '0');
    return Scaffold(
      appBar: AppBar(title: const Text('Record Voice')),
      body: Center(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          Text('$mm:$ss', style: Theme.of(context).textTheme.displayMedium?.copyWith(fontFeatures: const [FontFeature.tabularFigures()])),
          const SizedBox(height: 8),
          Text(_recording ? 'Recording… “Remember that I need to…”' : (_path == null ? 'Tap to start' : 'Recorded'),
              style: TextStyle(color: scheme.onSurfaceVariant)),
          const SizedBox(height: 32),
          SizedBox(
            width: 88,
            height: 88,
            child: FilledButton(
              style: FilledButton.styleFrom(shape: const CircleBorder(), backgroundColor: _recording ? scheme.error : scheme.primary),
              onPressed: _recording ? _stop : _start,
              child: Icon(_recording ? Icons.stop : Icons.mic, size: 40),
            ),
          ),
          if (_path != null && !_recording) ...[
            const SizedBox(height: 32),
            Row(mainAxisSize: MainAxisSize.min, children: [
              OutlinedButton(onPressed: _start, child: const Text('Re-record')),
              const SizedBox(width: 12),
              FilledButton(onPressed: _save, child: const Text('Save voice note')),
            ]),
          ],
          if (_error != null) Padding(padding: const EdgeInsets.all(16), child: Text(_error!, style: TextStyle(color: scheme.error))),
        ]),
      ),
    );
  }
}
