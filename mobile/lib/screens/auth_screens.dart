import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import 'package:supabase_flutter/supabase_flutter.dart';

import '../config.dart';

class SplashScreen extends StatelessWidget {
  const SplashScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Scaffold(
      body: Center(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          Container(
            width: 72,
            height: 72,
            decoration: BoxDecoration(color: scheme.primary, borderRadius: BorderRadius.circular(20)),
            alignment: Alignment.center,
            child: Text('M', style: TextStyle(color: scheme.onPrimary, fontSize: 36, fontWeight: FontWeight.bold)),
          ),
          const SizedBox(height: 16),
          Text('Memory', style: Theme.of(context).textTheme.headlineSmall?.copyWith(fontWeight: FontWeight.bold)),
        ]),
      ),
    );
  }
}

class WelcomeScreen extends StatelessWidget {
  const WelcomeScreen({super.key});

  @override
  Widget build(BuildContext context) {
    final t = Theme.of(context).textTheme;
    return Scaffold(
      body: SafeArea(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
            const Spacer(),
            Text('Memory', style: t.displaySmall?.copyWith(fontWeight: FontWeight.bold)),
            const SizedBox(height: 8),
            Text('Save anything. Find anything.', style: t.titleLarge),
            const SizedBox(height: 16),
            Text(
              'Screenshots, receipts, documents, voice notes and links — understood automatically, private by default, '
              'and found again by simply asking.',
              style: t.bodyLarge?.copyWith(color: Theme.of(context).colorScheme.onSurfaceVariant),
            ),
            const Spacer(),
            if (!Config.isConfigured)
              const Padding(
                padding: EdgeInsets.only(bottom: 12),
                child: Text('Build is missing SUPABASE_ANON_KEY (see mobile/README).', style: TextStyle(color: Colors.red)),
              ),
            FilledButton(onPressed: () => context.push('/register'), child: const Text('Create account')),
            const SizedBox(height: 12),
            OutlinedButton(onPressed: () => context.push('/login'), child: const Text('Sign in')),
          ]),
        ),
      ),
    );
  }
}

Future<void> _oauth(BuildContext context, OAuthProvider provider) async {
  try {
    await Supabase.instance.client.auth.signInWithOAuth(provider, redirectTo: Config.authRedirect);
  } on AuthException catch (e) {
    if (context.mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
  }
}

class _OAuthButtons extends StatelessWidget {
  const _OAuthButtons();

  @override
  Widget build(BuildContext context) {
    final isApple = Theme.of(context).platform == TargetPlatform.iOS || Theme.of(context).platform == TargetPlatform.macOS;
    return Column(crossAxisAlignment: CrossAxisAlignment.stretch, children: [
      OutlinedButton.icon(
        onPressed: () => _oauth(context, OAuthProvider.google),
        icon: const Icon(Icons.g_mobiledata, size: 28),
        label: const Text('Continue with Google'),
      ),
      if (isApple) ...[
        const SizedBox(height: 8),
        OutlinedButton.icon(
          onPressed: () => _oauth(context, OAuthProvider.apple),
          icon: const Icon(Icons.apple),
          label: const Text('Continue with Apple'),
        ),
      ],
      const Padding(padding: EdgeInsets.symmetric(vertical: 12), child: Row(children: [
        Expanded(child: Divider()),
        Padding(padding: EdgeInsets.symmetric(horizontal: 8), child: Text('or')),
        Expanded(child: Divider()),
      ])),
    ]);
  }
}

class LoginScreen extends StatefulWidget {
  const LoginScreen({super.key});
  @override
  State<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends State<LoginScreen> {
  final _email = TextEditingController();
  final _password = TextEditingController();
  bool _busy = false;
  String? _error;

  Future<void> _submit() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await Supabase.instance.client.auth.signInWithPassword(email: _email.text.trim(), password: _password.text);
    } on AuthException catch (e) {
      setState(() => _error = e.message == 'Invalid login credentials' ? 'Wrong email or password.' : e.message);
    } catch (_) {
      setState(() => _error = 'Could not reach the server. Check your connection.');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Sign in')),
      body: ListView(padding: const EdgeInsets.all(24), children: [
        const _OAuthButtons(),
        TextField(
          controller: _email,
          keyboardType: TextInputType.emailAddress,
          autofillHints: const [AutofillHints.email],
          decoration: const InputDecoration(labelText: 'Email'),
        ),
        const SizedBox(height: 12),
        TextField(
          controller: _password,
          obscureText: true,
          autofillHints: const [AutofillHints.password],
          decoration: const InputDecoration(labelText: 'Password'),
          onSubmitted: (_) => _submit(),
        ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 12), child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error))),
        const SizedBox(height: 20),
        FilledButton(onPressed: _busy ? null : _submit, child: Text(_busy ? 'Signing in…' : 'Sign in')),
        TextButton(onPressed: () => context.pushReplacement('/register'), child: const Text('New to Memory? Create an account')),
      ]),
    );
  }
}

class RegisterScreen extends StatefulWidget {
  const RegisterScreen({super.key});
  @override
  State<RegisterScreen> createState() => _RegisterScreenState();
}

class _RegisterScreenState extends State<RegisterScreen> {
  final _name = TextEditingController();
  final _email = TextEditingController();
  final _password = TextEditingController();
  bool _busy = false;
  bool _sent = false;
  String? _error;

  Future<void> _submit() async {
    if (_password.text.length < 8) {
      setState(() => _error = 'Use at least 8 characters for your password.');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final res = await Supabase.instance.client.auth.signUp(
        email: _email.text.trim(),
        password: _password.text,
        data: {'name': _name.text.trim()},
        emailRedirectTo: Config.authRedirect,
      );
      if (res.session == null) setState(() => _sent = true);
    } on AuthException catch (e) {
      setState(() => _error = e.message);
    } catch (_) {
      setState(() => _error = 'Could not reach the server. Check your connection.');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_sent) {
      return Scaffold(
        appBar: AppBar(title: const Text('Check your email')),
        body: Padding(
          padding: const EdgeInsets.all(24),
          child: Text('We sent a confirmation link to ${_email.text}. Open it on this phone to finish creating your account.'),
        ),
      );
    }
    return Scaffold(
      appBar: AppBar(title: const Text('Create account')),
      body: ListView(padding: const EdgeInsets.all(24), children: [
        const _OAuthButtons(),
        TextField(controller: _name, decoration: const InputDecoration(labelText: 'Name'), autofillHints: const [AutofillHints.name]),
        const SizedBox(height: 12),
        TextField(
          controller: _email,
          keyboardType: TextInputType.emailAddress,
          decoration: const InputDecoration(labelText: 'Email'),
          autofillHints: const [AutofillHints.email],
        ),
        const SizedBox(height: 12),
        TextField(
          controller: _password,
          obscureText: true,
          decoration: const InputDecoration(labelText: 'Password (8+ characters)'),
          autofillHints: const [AutofillHints.newPassword],
        ),
        if (_error != null) Padding(padding: const EdgeInsets.only(top: 12), child: Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error))),
        const SizedBox(height: 20),
        FilledButton(onPressed: _busy ? null : _submit, child: Text(_busy ? 'Creating…' : 'Create account')),
        const SizedBox(height: 12),
        Text('Your memories are private. Only you can see them.',
            textAlign: TextAlign.center, style: Theme.of(context).textTheme.bodySmall),
      ]),
    );
  }
}
