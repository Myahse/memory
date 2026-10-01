# Memory — Flutter app

Capture + offline queue + display. All intelligence runs in the cloud backend.

```bash
flutter pub get
flutter run \
  --dart-define=SUPABASE_URL=https://<project>.supabase.co \
  --dart-define=SUPABASE_ANON_KEY=<anon/publishable key> \
  --dart-define=API_URL=https://api.example.com
```

* OAuth (Google / Apple) and email confirmation return to
  `app.memory://login-callback` — add it to Supabase *Auth → URL configuration*.
* Permissions: camera, photo library and microphone (declared in
  `ios/Runner/Info.plist` and `android/app/src/main/AndroidManifest.xml`).
* Screens: splash, welcome, login, register, home (search, add, sync status,
  offline queue, recent), add memory (take photo, choose photo, upload document,
  record voice, write note, save link), memory detail (preview, extracted text,
  tags, details, related, edit, share, download, delete, retry), search with
  filters, Ask Memory chat with sources, timeline, profile, settings (theme,
  sign out, delete my data).

```bash
flutter analyze && flutter test
```
