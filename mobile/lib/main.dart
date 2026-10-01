import 'dart:async';

import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:supabase_flutter/supabase_flutter.dart';

import 'config.dart';
import 'screens/add_memory.dart';
import 'screens/ask_screen.dart';
import 'screens/auth_screens.dart';
import 'screens/home_screen.dart';
import 'screens/memory_detail.dart';
import 'screens/profile_settings.dart';
import 'screens/search_screen.dart';
import 'screens/timeline_screen.dart';
import 'services/app_state.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  runApp(const MemoryApp());
}

/// Signals the router when initialization or auth state changes.
class _AppBoot extends ChangeNotifier {
  bool ready = false;
  StreamSubscription<AuthState>? _sub;

  Future<void> start() async {
    await Supabase.initialize(url: Config.supabaseUrl, publishableKey: Config.supabaseAnonKey.isEmpty ? 'missing' : Config.supabaseAnonKey);
    await AppState.instance.init();
    _sub = Supabase.instance.client.auth.onAuthStateChange.listen((_) => notifyListeners());
    ready = true;
    notifyListeners();
  }

  bool get signedIn => ready && Supabase.instance.client.auth.currentSession != null;

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }
}

class MemoryApp extends StatefulWidget {
  const MemoryApp({super.key});
  @override
  State<MemoryApp> createState() => _MemoryAppState();
}

class _MemoryAppState extends State<MemoryApp> {
  final _boot = _AppBoot();
  late final GoRouter _router = _buildRouter();

  @override
  void initState() {
    super.initState();
    _boot.start();
  }

  GoRouter _buildRouter() => GoRouter(
        initialLocation: '/splash',
        refreshListenable: _boot,
        redirect: (context, state) {
          final loc = state.matchedLocation;
          if (!_boot.ready) return loc == '/splash' ? null : '/splash';
          const public = {'/welcome', '/login', '/register'};
          if (!_boot.signedIn) return public.contains(loc) ? null : '/welcome';
          if (public.contains(loc) || loc == '/splash') return '/';
          return null;
        },
        routes: [
          GoRoute(path: '/splash', builder: (_, _) => const SplashScreen()),
          GoRoute(path: '/welcome', builder: (_, _) => const WelcomeScreen()),
          GoRoute(path: '/login', builder: (_, _) => const LoginScreen()),
          GoRoute(path: '/register', builder: (_, _) => const RegisterScreen()),
          StatefulShellRoute.indexedStack(
            builder: (context, state, shell) => _Shell(shell: shell),
            branches: [
              StatefulShellBranch(routes: [GoRoute(path: '/', builder: (_, _) => const HomeScreen())]),
              StatefulShellBranch(routes: [GoRoute(path: '/chat', builder: (_, _) => const AskScreen())]),
              StatefulShellBranch(routes: [GoRoute(path: '/timeline', builder: (_, _) => const TimelineScreen())]),
              StatefulShellBranch(routes: [GoRoute(path: '/profile', builder: (_, _) => const ProfileScreen())]),
            ],
          ),
          GoRoute(path: '/search', builder: (_, s) => SearchScreen(initialQuery: s.uri.queryParameters['q'] ?? '')),
          GoRoute(path: '/ask', builder: (_, s) => AskScreen(seed: s.extra as AskSeed?)),
          GoRoute(path: '/memory/:id', builder: (_, s) => MemoryDetailScreen(id: s.pathParameters['id']!)),
          GoRoute(path: '/add/note', builder: (_, _) => const NoteScreen()),
          GoRoute(path: '/add/link', builder: (_, _) => const LinkScreen()),
          GoRoute(path: '/add/voice', builder: (_, _) => const VoiceRecorderScreen()),
          GoRoute(path: '/settings', builder: (_, _) => const SettingsScreen()),
        ],
      );

  @override
  Widget build(BuildContext context) {
    const seed = Color(0xFF6D5DFC);
    ThemeData theme(Brightness b) {
      final scheme = ColorScheme.fromSeed(seedColor: seed, brightness: b);
      return ThemeData(
        colorScheme: scheme,
        useMaterial3: true,
        cardTheme: CardThemeData(
          elevation: 0,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(16),
            side: BorderSide(color: scheme.outlineVariant.withValues(alpha: 0.6)),
          ),
        ),
        inputDecorationTheme: InputDecorationTheme(
          filled: true,
          fillColor: scheme.surfaceContainerHighest.withValues(alpha: 0.5),
          border: OutlineInputBorder(borderRadius: BorderRadius.circular(14), borderSide: BorderSide.none),
        ),
        filledButtonTheme: FilledButtonThemeData(
          style: FilledButton.styleFrom(
            minimumSize: const Size(0, 48),
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
          ),
        ),
        outlinedButtonTheme: OutlinedButtonThemeData(
          style: OutlinedButton.styleFrom(
            minimumSize: const Size(0, 48),
            shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(14)),
          ),
        ),
      );
    }

    return ListenableBuilder(
      listenable: AppState.instance.theme,
      builder: (context, _) => MaterialApp.router(
        title: 'Memory',
        debugShowCheckedModeBanner: false,
        scaffoldMessengerKey: AppState.instance.messengerKey,
        theme: theme(Brightness.light),
        darkTheme: theme(Brightness.dark),
        themeMode: AppState.instance.theme.mode,
        routerConfig: _router,
      ),
    );
  }
}

class _Shell extends StatelessWidget {
  const _Shell({required this.shell});
  final StatefulNavigationShell shell;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: shell,
      floatingActionButton: shell.currentIndex == 0
          ? null
          : FloatingActionButton(
              tooltip: 'Add Memory',
              onPressed: () => showAddMemorySheet(context),
              child: const Icon(Icons.add),
            ),
      bottomNavigationBar: NavigationBar(
        selectedIndex: shell.currentIndex,
        onDestinationSelected: (i) => shell.goBranch(i, initialLocation: i == shell.currentIndex),
        destinations: const [
          NavigationDestination(icon: Icon(Icons.home_outlined), selectedIcon: Icon(Icons.home), label: 'Home'),
          NavigationDestination(icon: Icon(Icons.chat_bubble_outline), selectedIcon: Icon(Icons.chat_bubble), label: 'Ask'),
          NavigationDestination(icon: Icon(Icons.calendar_month_outlined), selectedIcon: Icon(Icons.calendar_month), label: 'Timeline'),
          NavigationDestination(icon: Icon(Icons.person_outline), selectedIcon: Icon(Icons.person), label: 'Profile'),
        ],
      ),
    );
  }
}
