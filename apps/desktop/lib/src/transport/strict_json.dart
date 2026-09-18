// Strict JSON decoding with Zatiti wire strictness.
//
// `dart:convert` silently keeps the last of two duplicate keys and turns an
// integer that overflows int64 into a double. Both are wire violations for
// Zatiti: a duplicated key can hide the value a human reviewed, and a rounded
// integer can change a version or an amount. This decoder refuses duplicate
// keys, trailing data, integers outside int64, malformed UTF-8, lone
// surrogate escapes and excessive nesting.

import 'dart:convert';
import 'dart:typed_data';

/// A violation of Zatiti wire strictness.
class StrictJsonException implements Exception {
  StrictJsonException(this.message, [this.offset]);

  final String message;
  final int? offset;

  @override
  String toString() => offset == null
      ? 'StrictJsonException: $message'
      : 'StrictJsonException: $message (at offset $offset)';
}

/// Maximum nesting depth accepted in one document.
const int maxJsonDepth = 64;

/// Decodes [bytes] as one strict JSON document.
Object? decodeStrictJsonBytes(List<int> bytes) {
  final String text;
  try {
    text = const Utf8Decoder(
      allowMalformed: false,
    ).convert(bytes is Uint8List ? bytes : Uint8List.fromList(bytes));
  } on FormatException catch (e) {
    throw StrictJsonException('body is not valid UTF-8: ${e.message}');
  }
  return decodeStrictJson(text);
}

/// Decodes [text] as one strict JSON document.
Object? decodeStrictJson(String text) {
  final parser = _Parser(text);
  parser.skipWhitespace();
  final value = parser.parseValue(0);
  parser.skipWhitespace();
  if (!parser.atEnd) {
    throw StrictJsonException('trailing data after JSON document', parser.pos);
  }
  return value;
}

class _Parser {
  _Parser(this.src);

  final String src;
  int pos = 0;

  bool get atEnd => pos >= src.length;

  void skipWhitespace() {
    while (pos < src.length) {
      final c = src.codeUnitAt(pos);
      if (c == 0x20 || c == 0x0A || c == 0x0D || c == 0x09) {
        pos++;
      } else {
        return;
      }
    }
  }

  Never fail(String message) => throw StrictJsonException(message, pos);

  Object? parseValue(int depth) {
    if (depth > maxJsonDepth) fail('nesting exceeds $maxJsonDepth levels');
    if (atEnd) fail('unexpected end of document');
    final c = src.codeUnitAt(pos);
    switch (c) {
      case 0x7B: // {
        return parseObject(depth);
      case 0x5B: // [
        return parseArray(depth);
      case 0x22: // "
        return parseString();
      case 0x74: // t
        expectLiteral('true');
        return true;
      case 0x66: // f
        expectLiteral('false');
        return false;
      case 0x6E: // n
        expectLiteral('null');
        return null;
      default:
        if (c == 0x2D || (c >= 0x30 && c <= 0x39)) return parseNumber();
        fail('unexpected character');
    }
  }

  void expectLiteral(String literal) {
    if (!src.startsWith(literal, pos)) fail('invalid literal');
    pos += literal.length;
  }

  Map<String, Object?> parseObject(int depth) {
    pos++; // {
    final out = <String, Object?>{};
    skipWhitespace();
    if (!atEnd && src.codeUnitAt(pos) == 0x7D) {
      pos++;
      return out;
    }
    while (true) {
      skipWhitespace();
      if (atEnd || src.codeUnitAt(pos) != 0x22) fail('expected object key');
      final keyOffset = pos;
      final key = parseString();
      if (out.containsKey(key)) {
        throw StrictJsonException('duplicate object key "$key"', keyOffset);
      }
      skipWhitespace();
      if (atEnd || src.codeUnitAt(pos) != 0x3A) fail('expected ":"');
      pos++;
      skipWhitespace();
      out[key] = parseValue(depth + 1);
      skipWhitespace();
      if (atEnd) fail('unterminated object');
      final c = src.codeUnitAt(pos);
      pos++;
      if (c == 0x2C) continue;
      if (c == 0x7D) return out;
      pos--;
      fail('expected "," or "}"');
    }
  }

  List<Object?> parseArray(int depth) {
    pos++; // [
    final out = <Object?>[];
    skipWhitespace();
    if (!atEnd && src.codeUnitAt(pos) == 0x5D) {
      pos++;
      return out;
    }
    while (true) {
      skipWhitespace();
      out.add(parseValue(depth + 1));
      skipWhitespace();
      if (atEnd) fail('unterminated array');
      final c = src.codeUnitAt(pos);
      pos++;
      if (c == 0x2C) continue;
      if (c == 0x5D) return out;
      pos--;
      fail('expected "," or "]"');
    }
  }

  String parseString() {
    pos++; // opening quote
    final buf = StringBuffer();
    while (true) {
      if (atEnd) fail('unterminated string');
      final c = src.codeUnitAt(pos);
      if (c == 0x22) {
        pos++;
        return buf.toString();
      }
      if (c < 0x20) fail('unescaped control character in string');
      if (c != 0x5C) {
        buf.writeCharCode(c);
        pos++;
        continue;
      }
      pos++;
      if (atEnd) fail('unterminated escape');
      final e = src.codeUnitAt(pos);
      pos++;
      switch (e) {
        case 0x22:
          buf.write('"');
        case 0x5C:
          buf.write(r'\');
        case 0x2F:
          buf.write('/');
        case 0x62:
          buf.write('\b');
        case 0x66:
          buf.write('\f');
        case 0x6E:
          buf.write('\n');
        case 0x72:
          buf.write('\r');
        case 0x74:
          buf.write('\t');
        case 0x75:
          final unit = parseHex4();
          if (unit >= 0xD800 && unit <= 0xDBFF) {
            if (!src.startsWith(r'\u', pos)) fail('lone high surrogate');
            pos += 2;
            final low = parseHex4();
            if (low < 0xDC00 || low > 0xDFFF) fail('lone high surrogate');
            buf.writeCharCode(unit);
            buf.writeCharCode(low);
          } else if (unit >= 0xDC00 && unit <= 0xDFFF) {
            fail('lone low surrogate');
          } else {
            buf.writeCharCode(unit);
          }
        default:
          pos--;
          fail('invalid escape');
      }
    }
  }

  int parseHex4() {
    if (pos + 4 > src.length) fail('truncated unicode escape');
    final v = int.tryParse(src.substring(pos, pos + 4), radix: 16);
    if (v == null) fail('invalid unicode escape');
    pos += 4;
    return v;
  }

  num parseNumber() {
    final start = pos;
    if (src.codeUnitAt(pos) == 0x2D) pos++;
    if (atEnd) fail('truncated number');
    final first = src.codeUnitAt(pos);
    if (first == 0x30) {
      pos++;
    } else if (first >= 0x31 && first <= 0x39) {
      while (!atEnd && _isDigit(src.codeUnitAt(pos))) {
        pos++;
      }
    } else {
      fail('invalid number');
    }
    var isInteger = true;
    if (!atEnd && src.codeUnitAt(pos) == 0x2E) {
      isInteger = false;
      pos++;
      if (atEnd || !_isDigit(src.codeUnitAt(pos))) fail('invalid fraction');
      while (!atEnd && _isDigit(src.codeUnitAt(pos))) {
        pos++;
      }
    }
    if (!atEnd && (src.codeUnitAt(pos) | 0x20) == 0x65) {
      isInteger = false;
      pos++;
      if (!atEnd &&
          (src.codeUnitAt(pos) == 0x2B || src.codeUnitAt(pos) == 0x2D)) {
        pos++;
      }
      if (atEnd || !_isDigit(src.codeUnitAt(pos))) fail('invalid exponent');
      while (!atEnd && _isDigit(src.codeUnitAt(pos))) {
        pos++;
      }
    }
    final literal = src.substring(start, pos);
    if (isInteger) {
      final big = BigInt.parse(literal);
      if (big < _minInt64 || big > _maxInt64) {
        throw StrictJsonException('integer $literal is outside int64', start);
      }
      return big.toInt();
    }
    final d = double.parse(literal);
    if (!d.isFinite) {
      throw StrictJsonException('number $literal is not finite', start);
    }
    return d;
  }

  static bool _isDigit(int c) => c >= 0x30 && c <= 0x39;
}

final BigInt _maxInt64 = BigInt.parse('9223372036854775807');
final BigInt _minInt64 = BigInt.parse('-9223372036854775808');

/// Reads one JSON object with every field accounted for.
///
/// Callers take each field they understand and then call [finish], which
/// refuses any field that was never read. A client that shows a human an
/// exact review must not quietly drop a field it does not understand.
class StrictObject {
  StrictObject(Object? value, this.context)
    : _map = value is Map<String, Object?>
          ? value
          : throw StrictJsonException('$context must be a JSON object');

  final Map<String, Object?> _map;
  final String context;
  final Set<String> _read = <String>{};

  bool has(String key) => _map.containsKey(key);

  Object? _take(String key, {required bool required}) {
    _read.add(key);
    if (!_map.containsKey(key)) {
      if (required) {
        throw StrictJsonException('$context is missing required "$key"');
      }
      return null;
    }
    return _map[key];
  }

  T _typed<T>(String key, Object? v) {
    if (v is T) return v;
    throw StrictJsonException('$context field "$key" has the wrong type');
  }

  String string(String key) => _typed<String>(key, _take(key, required: true));

  String? optionalString(String key) {
    final v = _take(key, required: false);
    return v == null ? null : _typed<String>(key, v);
  }

  int integer(String key) => _typed<int>(key, _take(key, required: true));

  int? optionalInteger(String key) {
    final v = _take(key, required: false);
    return v == null ? null : _typed<int>(key, v);
  }

  bool boolean(String key) => _typed<bool>(key, _take(key, required: true));

  bool? optionalBoolean(String key) {
    final v = _take(key, required: false);
    return v == null ? null : _typed<bool>(key, v);
  }

  DateTime dateTime(String key) => _parseTime(key, string(key));

  DateTime? optionalDateTime(String key) {
    final v = optionalString(key);
    return v == null ? null : _parseTime(key, v);
  }

  DateTime _parseTime(String key, String raw) {
    final t = DateTime.tryParse(raw);
    if (t == null) {
      throw StrictJsonException('$context field "$key" is not a date-time');
    }
    return t.toUtc();
  }

  /// A required field whose value may be JSON null.
  Object? nullable(String key) => _take(key, required: true);

  /// An optional field of any JSON type, returned untouched.
  Object? optional(String key) => _take(key, required: false);

  Map<String, Object?> object(String key) =>
      _typed<Map<String, Object?>>(key, _take(key, required: true));

  Map<String, Object?>? optionalObject(String key) {
    final v = _take(key, required: false);
    return v == null ? null : _typed<Map<String, Object?>>(key, v);
  }

  List<Object?> list(String key) =>
      _typed<List<Object?>>(key, _take(key, required: true));

  List<String> stringList(String key) => [
    for (final v in list(key)) _typed<String>(key, v),
  ];

  /// Refuses any field that no accessor read.
  void finish() {
    final unknown = _map.keys.where((k) => !_read.contains(k)).toList();
    if (unknown.isNotEmpty) {
      throw StrictJsonException(
        '$context carries unknown field(s): ${unknown.join(', ')}',
      );
    }
  }
}
