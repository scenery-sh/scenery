//! `.gitignore` rules for the development input scan.
//!
//! The CLI hashes the same tree with Go's `internal/watchignore`, and both
//! sides must select the same files or their source digests differ. This is
//! therefore a port of that matcher, including where it departs from Git:
//! the shared fixture `cmd/scenery/testdata/worker-input-scope.json` pins it.

/// One ignore rule, relative to the directory that declares it.
pub(crate) struct Rule {
    /// How many leading path segments belong to the declaring directory.
    base: usize,
    pattern: String,
    parts: Vec<String>,
    negated: bool,
    directory: bool,
    anchored: bool,
}

/// Parses the rules of one `.gitignore` declared `base` segments deep.
pub(crate) fn parse(base: usize, text: &str) -> Vec<Rule> {
    text.split('\n')
        .filter_map(|line| rule(base, line))
        .collect()
}

fn rule(base: usize, line: &str) -> Option<Rule> {
    let mut line = line.strip_suffix('\r').unwrap_or(line);
    // A trailing space counts only when a backslash escapes it.
    while let Some(rest) = line.strip_suffix(' ') {
        let escapes = rest.bytes().rev().take_while(|byte| *byte == b'\\').count();
        if escapes % 2 == 1 {
            break;
        }
        line = rest;
    }
    if line.is_empty() {
        return None;
    }
    if line.starts_with("\\#") {
        line = &line[1..];
    } else if line.starts_with('#') {
        return None;
    }
    let mut negated = false;
    if line.starts_with("\\!") {
        line = &line[1..];
    } else if let Some(rest) = line.strip_prefix('!') {
        negated = true;
        line = rest;
    }
    if line.is_empty() {
        return None;
    }
    let directory = line.ends_with('/');
    let line = line.trim_end_matches('/');
    let line = line.strip_prefix('/').unwrap_or(line);
    if line.is_empty() {
        return None;
    }
    let pattern = clean(line);
    if pattern == "." {
        return None;
    }
    Some(Rule {
        base,
        parts: pattern.split('/').map(str::to_owned).collect(),
        anchored: pattern.contains('/'),
        pattern,
        negated,
        directory,
    })
}

/// Go's `path.Clean`: lexical, with `.` for the empty path.
fn clean(path: &str) -> String {
    let rooted = path.starts_with('/');
    let mut parts: Vec<&str> = Vec::new();
    for part in path.split('/') {
        match part {
            "" | "." => {}
            ".." => match parts.last() {
                Some(last) if *last != ".." => {
                    parts.pop();
                }
                _ if !rooted => parts.push(".."),
                _ => {}
            },
            _ => parts.push(part),
        }
    }
    let joined = parts.join("/");
    match (rooted, joined.is_empty()) {
        (true, _) => format!("/{joined}"),
        (false, true) => ".".into(),
        (false, false) => joined,
    }
}

impl Rule {
    /// Reports whether the rule names the entry itself, not a parent.
    fn matches(&self, path: &[&str], directory: bool) -> bool {
        let Some(sub) = path.get(self.base..).filter(|sub| !sub.is_empty()) else {
            return false;
        };
        let named = if self.anchored {
            path_matches(&self.parts, sub)
        } else {
            segment_matches(&self.pattern, sub[sub.len() - 1])
        };
        named && (!self.directory || directory)
    }
}

/// Decides one entry of a walk. `rules` holds the rules of every ancestor
/// directory, outermost first; `parent` is the highest rule that ignores an
/// ancestor, as `ancestor` returned it for the entry's directory. The last
/// rule that names the entry wins; an entry no rule names inherits the
/// verdict of its parents.
pub(crate) fn ignored(
    rules: &[Rule],
    path: &[&str],
    directory: bool,
    parent: Option<usize>,
) -> bool {
    let floor = parent.map_or(0, |index| index + 1);
    for rule in rules[floor..].iter().rev() {
        if rule.matches(path, directory) {
            return !rule.negated;
        }
    }
    parent.is_some()
}

/// Returns the highest rule that ignores the directory `path` or one of its
/// ancestors, for the entries inside it.
pub(crate) fn ancestor(rules: &[Rule], path: &[&str], parent: Option<usize>) -> Option<usize> {
    let floor = parent.map_or(0, |index| index + 1);
    (floor..rules.len())
        .rev()
        .find(|index| !rules[*index].negated && rules[*index].matches(path, true))
        .or(parent)
}

fn path_matches(pattern: &[String], path: &[&str]) -> bool {
    let Some((first, rest)) = pattern.split_first() else {
        return path.is_empty();
    };
    if first == "**" {
        return (0..=path.len()).any(|skip| path_matches(rest, &path[skip..]));
    }
    path.split_first()
        .is_some_and(|(name, tail)| segment_matches(first, name) && path_matches(rest, tail))
}

fn segment_matches(pattern: &str, name: &str) -> bool {
    if !pattern.contains(['*', '?', '[', '\\']) {
        return pattern == name;
    }
    // A malformed pattern only matches a file spelled exactly like it.
    glob(pattern.as_bytes(), name.as_bytes()).unwrap_or(pattern == name)
}

/// Go's `path.Match`. `None` is its `ErrBadPattern`.
fn glob(mut pattern: &[u8], mut name: &[u8]) -> Option<bool> {
    'pattern: while !pattern.is_empty() {
        let (star, chunk, rest) = scan_chunk(pattern);
        pattern = rest;
        if star && chunk.is_empty() {
            // A trailing star takes the rest of the name, up to a slash.
            return Some(!name.contains(&b'/'));
        }
        let (tail, matched) = match_chunk(chunk, name)?;
        // The last chunk must use up the name, or a star may still do so.
        if matched && (tail.is_empty() || !pattern.is_empty()) {
            name = tail;
            continue;
        }
        if star {
            for skip in 0..name.len() {
                if name[skip] == b'/' {
                    break;
                }
                let (tail, matched) = match_chunk(chunk, &name[skip + 1..])?;
                if matched {
                    if pattern.is_empty() && !tail.is_empty() {
                        continue;
                    }
                    name = tail;
                    continue 'pattern;
                }
            }
        }
        // A pattern that cannot match must still be well formed.
        while !pattern.is_empty() {
            let (_, chunk, rest) = scan_chunk(pattern);
            pattern = rest;
            match_chunk(chunk, b"")?;
        }
        return Some(false);
    }
    Some(name.is_empty())
}

/// Splits off the leading stars and the literal chunk that follows them.
fn scan_chunk(mut pattern: &[u8]) -> (bool, &[u8], &[u8]) {
    let mut star = false;
    while pattern.first() == Some(&b'*') {
        pattern = &pattern[1..];
        star = true;
    }
    let mut class = false;
    let mut index = 0;
    while index < pattern.len() {
        match pattern[index] {
            b'\\' if index + 1 < pattern.len() => index += 1,
            b'[' => class = true,
            b']' => class = false,
            b'*' if !class => break,
            _ => {}
        }
        index += 1;
    }
    (star, &pattern[..index], &pattern[index..])
}

/// Matches `chunk` at the start of `name` and returns what is left of it.
/// A failed match still reads the whole chunk to validate it.
fn match_chunk<'a>(mut chunk: &[u8], mut name: &'a [u8]) -> Option<(&'a [u8], bool)> {
    let mut failed = false;
    while !chunk.is_empty() {
        if !failed && name.is_empty() {
            failed = true;
        }
        match chunk[0] {
            b'[' => {
                let mut actual = '\0';
                if !failed {
                    let (character, width) = decode(name);
                    actual = character;
                    name = &name[width..];
                }
                chunk = &chunk[1..];
                let negated = chunk.first() == Some(&b'^');
                if negated {
                    chunk = &chunk[1..];
                }
                let mut matched = false;
                let mut ranges = 0;
                loop {
                    if chunk.first() == Some(&b']') && ranges > 0 {
                        chunk = &chunk[1..];
                        break;
                    }
                    let (low, rest) = class_character(chunk)?;
                    chunk = rest;
                    let mut high = low;
                    if chunk.first() == Some(&b'-') {
                        let (bound, rest) = class_character(&chunk[1..])?;
                        high = bound;
                        chunk = rest;
                    }
                    if low <= actual && actual <= high {
                        matched = true;
                    }
                    ranges += 1;
                }
                if matched == negated {
                    failed = true;
                }
            }
            b'?' => {
                if !failed {
                    if name[0] == b'/' {
                        failed = true;
                    }
                    let (_, width) = decode(name);
                    name = &name[width..];
                }
                chunk = &chunk[1..];
            }
            byte => {
                if byte == b'\\' {
                    chunk = &chunk[1..];
                    if chunk.is_empty() {
                        return None;
                    }
                }
                if !failed {
                    if chunk[0] != name[0] {
                        failed = true;
                    }
                    name = &name[1..];
                }
                chunk = &chunk[1..];
            }
        }
    }
    Some(if failed { (b"", false) } else { (name, true) })
}

/// Reads one character of a class. The class must go on after it.
fn class_character(mut chunk: &[u8]) -> Option<(char, &[u8])> {
    if matches!(chunk.first(), None | Some(b'-' | b']')) {
        return None;
    }
    if chunk[0] == b'\\' {
        chunk = &chunk[1..];
        if chunk.is_empty() {
            return None;
        }
    }
    let (character, width) = decode(chunk);
    if character == char::REPLACEMENT_CHARACTER && width == 1 {
        return None;
    }
    let rest = &chunk[width..];
    (!rest.is_empty()).then_some((character, rest))
}

/// Decodes the first UTF-8 character like Go's `utf8.DecodeRune`: invalid
/// input is the replacement character, one byte wide.
fn decode(bytes: &[u8]) -> (char, usize) {
    let width = match bytes.first() {
        None => return (char::REPLACEMENT_CHARACTER, 0),
        Some(0x00..=0x7f) => 1,
        Some(0xc2..=0xdf) => 2,
        Some(0xe0..=0xef) => 3,
        Some(0xf0..=0xf4) => 4,
        Some(_) => return (char::REPLACEMENT_CHARACTER, 1),
    };
    bytes
        .get(..width)
        .and_then(|prefix| std::str::from_utf8(prefix).ok())
        .and_then(|text| text.chars().next())
        .map_or((char::REPLACEMENT_CHARACTER, 1), |character| {
            (character, width)
        })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn globs_follow_go_path_match() {
        for (pattern, name, expected) in [
            ("abc", "abc", Some(true)),
            ("*", "abc", Some(true)),
            ("*c", "abc", Some(true)),
            ("a*", "a", Some(true)),
            ("a*", "abc", Some(true)),
            ("a*/b", "abc/b", Some(true)),
            ("a*/b", "a/c/b", Some(false)),
            ("a*b*c*d*e*/f", "axbxcxdxe/f", Some(true)),
            ("a*b?c*x", "abxbbxdbxebxczzx", Some(true)),
            ("a*b?c*x", "abxbbxdbxebxczzy", Some(false)),
            ("ab[c]", "abc", Some(true)),
            ("ab[b-d]", "abc", Some(true)),
            ("ab[e-g]", "abc", Some(false)),
            ("ab[^c]", "abc", Some(false)),
            ("ab[^b-d]", "abc", Some(false)),
            ("ab[^e-g]", "abc", Some(true)),
            ("a\\*b", "a*b", Some(true)),
            ("a\\*b", "ab", Some(false)),
            ("a?b", "a☺b", Some(true)),
            ("a[^a]b", "a☺b", Some(true)),
            ("a???b", "a☺b", Some(false)),
            ("a[^a][^a][^a]b", "a☺b", Some(false)),
            ("[a-ζ]*", "α", Some(true)),
            ("*[a-ζ]", "A", Some(false)),
            ("a?b", "a/b", Some(false)),
            ("a*b", "a/b", Some(false)),
            ("[\\]a]", "]", Some(true)),
            ("[\\-]", "-", Some(true)),
            ("[x\\-]", "x", Some(true)),
            ("[x\\-]", "-", Some(true)),
            ("[x\\-]", "z", Some(false)),
            ("[\\-x]", "x", Some(true)),
            ("[\\-x]", "-", Some(true)),
            ("[\\-x]", "a", Some(false)),
            ("[]a]", "]", None),
            ("[-]", "-", None),
            ("[x-]", "x", None),
            ("[x-]", "-", None),
            ("[x-]", "z", None),
            ("[-x]", "x", None),
            ("[-x]", "-", None),
            ("[-x]", "a", None),
            ("\\", "a", None),
            ("[a-b-c]", "a", None),
            ("[", "a", None),
            ("[^", "a", None),
            ("[^bc", "a", None),
            ("a[", "a", None),
            ("a[", "ab", None),
            ("a[", "x", None),
            ("a/b[", "x", None),
            ("*x", "xxx", Some(true)),
        ] {
            assert_eq!(
                glob(pattern.as_bytes(), name.as_bytes()),
                expected,
                "{pattern} against {name}"
            );
        }
    }

    #[test]
    fn rules_parse_like_the_go_matcher() {
        let parsed = parse(
            1,
            "# comment\n\n\\#literal\n!keep.log \n/anchored\nbuild/\n\
             a//b/./c\ntrailing\\ \n!\n/\n./\ndocs/**/drafts\r\n",
        );
        let summary: Vec<_> = parsed
            .iter()
            .map(|rule| {
                assert_eq!(rule.base, 1);
                (
                    rule.pattern.as_str(),
                    rule.negated,
                    rule.directory,
                    rule.anchored,
                )
            })
            .collect();
        assert_eq!(
            summary,
            [
                ("#literal", false, false, false),
                ("keep.log", true, false, false),
                // A leading slash is dropped, so the name matches at any depth.
                ("anchored", false, false, false),
                ("build", false, true, false),
                ("a/b/c", false, false, true),
                ("trailing\\ ", false, false, false),
                ("docs/**/drafts", false, false, true),
            ]
        );
    }

    #[test]
    fn the_last_rule_naming_an_entry_wins_and_parents_are_inherited() {
        let rules = parse(0, "*.log\n!keep.log\nvar/\n!var/\nout/**/cache\n");
        let entry = |path: &str, directory: bool, parent| {
            let parts: Vec<_> = path.split('/').collect();
            ignored(&rules, &parts, directory, parent)
        };
        assert!(entry("debug.log", false, None));
        assert!(!entry("keep.log", false, None));
        assert!(!entry("src/main.go", false, None));
        assert!(entry("out/a/b/cache", true, None));
        assert!(entry("out/cache", false, None));
        // `var/` is ignored and then named again by a negation: the directory
        // is walked, but its entries still inherit the ignoring rule.
        assert!(!entry("var", true, None));
        let var = ancestor(&rules, &["var"], None);
        assert_eq!(var, Some(2));
        assert!(entry("var/data.bin", false, var));
        assert_eq!(ancestor(&rules, &["src"], None), None);
    }
}
