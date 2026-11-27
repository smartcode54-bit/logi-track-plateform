import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

/// The static fence of the gotest code (spec mobile-go-core-loop N2, AC14): the Go client and the gotest app never
/// import Firebase or the Firestore repositories, and the dev/prod code never imports the gotest app except main.dart.
void main() {
  const forbidden = [
    'package:cloud_firestore/',
    'package:firebase_storage/',
    'package:cloud_functions/',
    'package:firebase_auth/',
    'package:firebase_core/',
    'package:firebase_app_check/',
    'core/services/cloud_functions_service.dart',
    'firebase_options.dart',
  ];
  // The one gotest file that may read Firebase Auth: the 3.x session the first 4.x launch exchanges (spec
  // mobile-auth-exchange R15). It may import exactly these, and nothing else from the forbidden list.
  const legacySessionFile = 'lib/go/features/auth/data/firebase_session_source.dart';
  const legacySessionAllowed = ['package:firebase_auth/', 'package:firebase_core/', 'firebase_options.dart'];
  // The one Firestore-repository file the gotest pages may import: for the SelectableTruck type only.
  const allowedRepository = 'trucks_repository.dart';

  Iterable<File> dartFiles(String dir) => Directory(dir)
      .listSync(recursive: true)
      .whereType<File>()
      .where((f) => f.path.endsWith('.dart'));

  List<String> imports(File f) => f
      .readAsLinesSync()
      .map((l) => l.trim())
      .where((l) => l.startsWith('import ') || l.startsWith('export '))
      .toList();

  test('lib/go and lib/core/api import no Firebase and no Firestore repository', () {
    final bad = <String>[];
    for (final dir in ['lib/go', 'lib/core/api']) {
      for (final f in dartFiles(dir)) {
        final legacy = f.path.replaceAll('\\', '/') == legacySessionFile;
        for (final line in imports(f)) {
          final allowed = legacy && legacySessionAllowed.any(line.contains);
          if (!allowed && forbidden.any(line.contains)) bad.add('${f.path}: $line');
          if (line.contains('/data/repositories/') &&
              !line.contains('/go/') &&
              !line.contains(allowedRepository) &&
              !RegExp(r"import '\.\./data/").hasMatch(line)) {
            bad.add('${f.path}: $line');
          }
        }
      }
    }
    expect(bad, isEmpty, reason: 'gotest code must stay off Firebase:\n${bad.join('\n')}');
  });

  test('only main.dart imports the gotest app', () {
    final bad = <String>[];
    for (final f in dartFiles('lib')) {
      final p = f.path.replaceAll('\\', '/');
      if (p.startsWith('lib/go/') || p == 'lib/main.dart') continue;
      for (final line in imports(f)) {
        if (line.contains('/go/') || line.contains("'go/")) bad.add('$p: $line');
      }
    }
    expect(bad, isEmpty, reason: 'dev/prod code must not reach the gotest app:\n${bad.join('\n')}');
  });
}
