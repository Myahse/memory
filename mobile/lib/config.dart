/// Build-time configuration, passed with --dart-define (see README):
///   flutter run --dart-define=SUPABASE_URL=... --dart-define=SUPABASE_ANON_KEY=... --dart-define=API_URL=...
class Config {
  static const supabaseUrl = String.fromEnvironment('SUPABASE_URL', defaultValue: 'http://10.0.2.2:54321');
  static const supabaseAnonKey = String.fromEnvironment('SUPABASE_ANON_KEY');
  static const apiUrl = String.fromEnvironment('API_URL', defaultValue: 'http://10.0.2.2:8080');

  /// Deep link used for OAuth (Google / Apple) and email confirmation.
  static const authRedirect = 'app.memory://login-callback';

  static bool get isConfigured => supabaseAnonKey.isNotEmpty;
}
