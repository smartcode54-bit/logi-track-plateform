-- 0026_search_fold.sql. Never edit this file once it is merged; follow-ups take the next number (R31).
-- Issue #142 (UI2B): search_fold(text), the search folding of the list endpoints in SQL. It is the twin of
-- internal/platform/searchtext.Fold (Go: the hub list and the folded words of every q) and of foldSearchText in
-- logitrack-web/lib/searchText.ts (the searchable select's local filter): NFKD (full-width letters, sara am),
-- drop the Latin combining marks U+0300-U+036F and the Thai marks U+0E47-U+0E4E (maitaikhu, the tone marks,
-- thanthakhat, nikhahit, yamakkan; vowel signs stay), Thai digits to ASCII, NFC, lower case. White space is not
-- collapsed here: the words of a query hold none, so a run of spaces in a haystack changes no match.
-- searchtext_integration_test.go checks this function against Fold case by case; change the three together.
--
-- The lists of drivers, trucks, customers and users (and the dispatcher's scope_drivers / scope_trucks reads) match
-- `search_fold(<searchable columns>) LIKE ALL (<the folded words of q as %word%>)` while they walk their keyset
-- index (created_at or code order, LIMIT): every word must be somewhere in the row, in any order. No index is built
-- on it: these masters hold hundreds to a few thousand rows per reach and a page stops after LIMIT matches; the
-- plan-shape test (cmd/seed, T61) pins that a search keeps the keyset index. A trigram index would also need
-- pg_trgm and a ctype that counts Thai letters as word characters.
--
-- IMMUTABLE (it may back an index or a generated column later): normalize(), regexp_replace(), translate() and
-- lower() are. lower() follows the database's LC_CTYPE (en_US.utf8 under the postgres:18-alpine initdb defaults);
-- Latin letters are decomposed before it, so a C ctype would still fold them, and Thai has no case.
-- No GRANT: EXECUTE on a plain function is PUBLIC by default (as bkk_date, 0001).

-- +goose Up
CREATE FUNCTION search_fold(s text) RETURNS text
  LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
  RETURN lower(normalize(translate(regexp_replace(normalize(s, NFKD), '[\u0300-\u036f\u0e47-\u0e4e]', '', 'g'),
                                   '๐๑๒๓๔๕๖๗๘๙', '0123456789'), NFC));

-- +goose Down
DROP FUNCTION search_fold(text);
