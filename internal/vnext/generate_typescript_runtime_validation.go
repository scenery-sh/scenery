package vnext

func renderTSRuntimeValidation() string {
	return `
interface ValidationNumber {
  readonly numerator: bigint;
  readonly denominator: bigint;
}

function validateRecordRules(
  value: Readonly<Record<string, unknown>>,
  descriptor: Extract<TypeDescriptor, { readonly kind: "record" }>,
  registry: TypeRegistry,
  path: string,
): void {
  for (const validation of descriptor.validations ?? []) {
    const result = evaluateValidation(validation.expression, value, descriptor, registry, path);
    if (typeof result !== "boolean") invalid(path, §validation ${validation.name} did not produce bool§);
    if (result) {
      throw new SceneryClientError(validation.code, "", §${validation.path || path}: ${validation.message}§);
    }
  }
}

function evaluateValidation(
  expression: ValidationExpression,
  record: Readonly<Record<string, unknown>>,
  descriptor: Extract<TypeDescriptor, { readonly kind: "record" }>,
  registry: TypeRegistry,
  path: string,
): unknown {
  switch (expression.kind) {
    case "literal":
      if (expression.type === "null") return null;
      if (expression.type === "number") return validationNumber(String(expression.value), path);
      return expression.value;
    case "value":
      return record;
    case "attribute": {
      if (expression.source.kind === "value") {
        const field = descriptor.fields.find((candidate) => candidate.property === expression.name);
        if (field === undefined) invalid(path, §validation references unknown field ${expression.name}§);
        return validationContractValue(record[expression.name], field.value, registry, path);
      }
      const source = evaluateValidation(expression.source, record, descriptor, registry, path);
      if (!isObject(source)) invalid(path, "validation attribute source is not an object");
      return source[expression.name];
    }
    case "index": {
      const collection = evaluateValidation(expression.collection, record, descriptor, registry, path);
      const key = evaluateValidation(expression.key, record, descriptor, registry, path);
      if (Array.isArray(collection)) {
        const index = validationInteger(key, path);
        if (index < 0 || index >= collection.length) invalid(path, "validation index is out of range");
        return collection[index];
      }
      if (!isObject(collection) || typeof key !== "string") invalid(path, "validation index requires a collection and matching key");
      return collection[key];
    }
    case "call":
      return evaluateValidationCall(
        expression.name,
        expression.arguments.map((argument) => evaluateValidation(argument, record, descriptor, registry, path)),
        path,
      );
    case "unary": {
      const value = evaluateValidation(expression.value, record, descriptor, registry, path);
      if (expression.operator === "!") {
        if (typeof value !== "boolean") invalid(path, "validation logical operand is not bool");
        return !value;
      }
      if (expression.operator === "-") return validationNumberNegate(requireValidationNumber(value, path));
      invalid(path, "unknown validation unary operator");
    }
    case "binary":
      if (expression.operator === "&&" || expression.operator === "||") {
        const left = evaluateValidation(expression.left, record, descriptor, registry, path);
        if (typeof left !== "boolean") invalid(path, "validation logical operand is not bool");
        if (expression.operator === "&&" && !left) return false;
        if (expression.operator === "||" && left) return true;
        const right = evaluateValidation(expression.right, record, descriptor, registry, path);
        if (typeof right !== "boolean") invalid(path, "validation logical operand is not bool");
        return right;
      }
      return evaluateValidationBinary(
        expression.operator,
        evaluateValidation(expression.left, record, descriptor, registry, path),
        evaluateValidation(expression.right, record, descriptor, registry, path),
        path,
      );
    case "conditional": {
      const condition = evaluateValidation(expression.condition, record, descriptor, registry, path);
      if (typeof condition !== "boolean") invalid(path, "validation condition is not bool");
      return evaluateValidation(condition ? expression.true_result : expression.false_result, record, descriptor, registry, path);
    }
    case "tuple":
      return expression.values.map((value) => evaluateValidation(value, record, descriptor, registry, path));
    case "object": {
      const result = Object.create(null) as Record<string, unknown>;
      for (const entry of expression.entries) {
        const key = evaluateValidation(entry.key, record, descriptor, registry, path);
        if (typeof key !== "string") invalid(path, "validation object key is not string");
        assertSafeKey(key);
        result[key] = evaluateValidation(entry.value, record, descriptor, registry, path);
      }
      return result;
    }
    case "template":
      return expression.parts.map((part) => validationString(evaluateValidation(part, record, descriptor, registry, path), path)).join("");
  }
}

function validationContractValue(value: unknown, descriptor: TypeDescriptor, registry: TypeRegistry, path: string): unknown {
  const resolved = resolveDescriptor(descriptor, registry, new Set());
  if (resolved.kind === "optional") return value === undefined ? null : validationContractValue(value, resolved.value, registry, path);
  if (resolved.kind === "nullable") return value === null ? null : validationContractValue(value, resolved.value, registry, path);
  if (resolved.kind === "list" || resolved.kind === "set") {
    if (!Array.isArray(value)) invalid(path, "validation collection value is invalid");
    return value.map((item) => validationContractValue(item, resolved.value, registry, path));
  }
  if (resolved.kind === "tuple") {
    if (!Array.isArray(value) || value.length !== resolved.values.length) invalid(path, "validation tuple value is invalid");
    return value.map((item, index) => validationContractValue(item, resolved.values[index]!, registry, path));
  }
  if (resolved.kind === "map") {
    if (!isObject(value) || Array.isArray(value)) invalid(path, "validation map value is invalid");
    const result = Object.create(null) as Record<string, unknown>;
    for (const [key, item] of Object.entries(value)) result[key] = validationContractValue(item, resolved.value, registry, path);
    return result;
  }
  if (resolved.kind !== "primitive") return value;
  switch (resolved.name) {
    case "int":
    case "int32":
    case "int64":
    case "uint32":
    case "uint64":
    case "size":
    case "decimal":
    case "float32":
    case "float64":
      return validationNumberFromValue(value, path);
    case "date":
      return validationDate(value, path);
    case "datetime":
      return validationDateTime(value, path);
    case "duration":
      return validationDuration(value, path);
    default:
      return value;
  }
}

function evaluateValidationCall(name: string, args: readonly unknown[], path: string): unknown {
  switch (name) {
    case "contains":
      if (args.length !== 2) invalid(path, "contains requires two arguments");
      if (typeof args[0] === "string" && typeof args[1] === "string") return args[0].includes(args[1]);
      if (Array.isArray(args[0])) return args[0].some((item) => validationEqual(item, args[1]));
      invalid(path, "contains requires a string or collection");
    case "length":
      if (args.length !== 1) invalid(path, "length requires one argument");
      if (typeof args[0] === "string") return validationNumber(String([...args[0]].length), path);
      if (Array.isArray(args[0])) return validationNumber(String(args[0].length), path);
      if (isObject(args[0])) return validationNumber(String(Object.keys(args[0]).length), path);
      invalid(path, "length requires a string or collection");
    case "starts_with":
    case "ends_with":
      if (args.length !== 2 || typeof args[0] !== "string" || typeof args[1] !== "string") invalid(path, §${name} requires two strings§);
      return name === "starts_with" ? args[0].startsWith(args[1]) : args[0].endsWith(args[1]);
    case "lower":
    case "upper":
      if (args.length !== 1 || typeof args[0] !== "string") invalid(path, §${name} requires one string§);
      return name === "lower" ? args[0].toLocaleLowerCase("und") : args[0].toLocaleUpperCase("und");
    case "format":
      if (args.length === 0 || typeof args[0] !== "string") invalid(path, "format requires a format string");
      return validationFormat(args[0], args.slice(1), path);
    default:
      invalid(path, §unknown validation function ${name}§);
  }
}

function evaluateValidationBinary(operator: string, left: unknown, right: unknown, path: string): unknown {
  switch (operator) {
    case "==": return validationEqual(left, right);
    case "!=": return !validationEqual(left, right);
    case "<": return validationCompare(left, right, path) < 0;
    case "<=": return validationCompare(left, right, path) <= 0;
    case ">": return validationCompare(left, right, path) > 0;
    case ">=": return validationCompare(left, right, path) >= 0;
    case "+": return validationNumberAdd(requireValidationNumber(left, path), requireValidationNumber(right, path));
    case "-": return validationNumberSubtract(requireValidationNumber(left, path), requireValidationNumber(right, path));
    case "*": return validationNumberMultiply(requireValidationNumber(left, path), requireValidationNumber(right, path));
    case "/": return validationNumberDivide(requireValidationNumber(left, path), requireValidationNumber(right, path), path);
    case "%": return validationNumberModulo(requireValidationNumber(left, path), requireValidationNumber(right, path), path);
    default: invalid(path, §unknown validation binary operator ${operator}§);
  }
}

function validationEqual(left: unknown, right: unknown): boolean {
  if (isValidationNumber(left) && isValidationNumber(right)) return left.numerator * right.denominator === right.numerator * left.denominator;
  if (left === null || right === null || typeof left !== "object" || typeof right !== "object") return Object.is(left, right);
  if (Array.isArray(left) || Array.isArray(right)) {
    return Array.isArray(left) && Array.isArray(right) && left.length === right.length && left.every((item, index) => validationEqual(item, right[index]));
  }
  const leftKeys = Object.keys(left as Record<string, unknown>).sort();
  const rightKeys = Object.keys(right as Record<string, unknown>).sort();
  return leftKeys.length === rightKeys.length && leftKeys.every((key, index) => key === rightKeys[index] && validationEqual((left as Record<string, unknown>)[key], (right as Record<string, unknown>)[key]));
}

function validationCompare(left: unknown, right: unknown, path: string): number {
  if (isValidationNumber(left) && isValidationNumber(right)) {
    const leftValue = left.numerator * right.denominator;
    const rightValue = right.numerator * left.denominator;
    return leftValue < rightValue ? -1 : leftValue > rightValue ? 1 : 0;
  }
  if (typeof left === "string" && typeof right === "string") return left < right ? -1 : left > right ? 1 : 0;
  invalid(path, "validation comparison operands have incompatible types");
}

function validationNumber(source: string, path: string): ValidationNumber {
  const parts = jsonNumberTokenParts(source, path);
  return validationNumberNormalize({ numerator: BigInt(parts.coefficient), denominator: 10n ** BigInt(parts.scale) });
}

function validationNumberFromValue(value: unknown, path: string): ValidationNumber {
  const parts = exactNumberParts(value, path);
  return validationNumberNormalize({ numerator: BigInt(parts.coefficient), denominator: 10n ** BigInt(parts.scale) });
}

function isValidationNumber(value: unknown): value is ValidationNumber {
  return isObject(value) && typeof value.numerator === "bigint" && typeof value.denominator === "bigint";
}

function requireValidationNumber(value: unknown, path: string): ValidationNumber {
  if (!isValidationNumber(value)) invalid(path, "validation numeric operand is not a number");
  return value;
}

function validationNumberNormalize(value: ValidationNumber): ValidationNumber {
  if (value.denominator === 0n) throw new SceneryClientError("invalid_input", "", "validation division by zero");
  let numerator = value.denominator < 0n ? -value.numerator : value.numerator;
  let denominator = value.denominator < 0n ? -value.denominator : value.denominator;
  const divisor = validationGreatestCommonDivisor(numerator < 0n ? -numerator : numerator, denominator);
  numerator /= divisor;
  denominator /= divisor;
  return { numerator, denominator };
}

function validationGreatestCommonDivisor(left: bigint, right: bigint): bigint {
  while (right !== 0n) [left, right] = [right, left % right];
  return left === 0n ? 1n : left;
}

function validationNumberNegate(value: ValidationNumber): ValidationNumber {
  return { numerator: -value.numerator, denominator: value.denominator };
}

function validationNumberAdd(left: ValidationNumber, right: ValidationNumber): ValidationNumber {
  return validationNumberNormalize({ numerator: left.numerator * right.denominator + right.numerator * left.denominator, denominator: left.denominator * right.denominator });
}

function validationNumberSubtract(left: ValidationNumber, right: ValidationNumber): ValidationNumber {
  return validationNumberNormalize({ numerator: left.numerator * right.denominator - right.numerator * left.denominator, denominator: left.denominator * right.denominator });
}

function validationNumberMultiply(left: ValidationNumber, right: ValidationNumber): ValidationNumber {
  return validationNumberNormalize({ numerator: left.numerator * right.numerator, denominator: left.denominator * right.denominator });
}

function validationNumberDivide(left: ValidationNumber, right: ValidationNumber, path: string): ValidationNumber {
  if (right.numerator === 0n) invalid(path, "validation division by zero");
  return validationNumberNormalize({ numerator: left.numerator * right.denominator, denominator: left.denominator * right.numerator });
}

function validationNumberModulo(left: ValidationNumber, right: ValidationNumber, path: string): ValidationNumber {
  if (right.numerator === 0n) invalid(path, "validation modulo by zero");
  const quotientNumerator = left.numerator * right.denominator;
  const quotientDenominator = left.denominator * right.numerator;
  const quotient = quotientNumerator / quotientDenominator;
  return validationNumberSubtract(left, validationNumberMultiply({ numerator: quotient, denominator: 1n }, right));
}

function validationInteger(value: unknown, path: string): number {
  const number = requireValidationNumber(value, path);
  if (number.denominator !== 1n || number.numerator < BigInt(Number.MIN_SAFE_INTEGER) || number.numerator > BigInt(Number.MAX_SAFE_INTEGER)) invalid(path, "validation index is not an integer");
  return Number(number.numerator);
}

function validationDate(value: unknown, path: string): ValidationNumber {
  if (typeof value !== "string" || !validCalendarDate(value)) invalid(path, "validation date is invalid");
  const [year, month, day] = value.split("-").map(Number);
  return { numerator: BigInt(validationCivilDays(year!, month!, day!)), denominator: 1n };
}

function validationDateTime(value: unknown, path: string): ValidationNumber {
  if (typeof value !== "string" || !validDateTime(value)) invalid(path, "validation datetime is invalid");
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/.exec(value)!;
  const seconds = BigInt(validationCivilDays(Number(match[1]), Number(match[2]), Number(match[3])) * 86_400 + Number(match[4]) * 3_600 + Number(match[5]) * 60 + Number(match[6]));
  const nanoseconds = BigInt((match[7] ?? "").padEnd(9, "0") || "0");
  return { numerator: seconds * 1_000_000_000n + nanoseconds, denominator: 1n };
}

function validationCivilDays(year: number, month: number, day: number): number {
  const adjustedYear = year - (month <= 2 ? 1 : 0);
  const era = Math.floor(adjustedYear / 400);
  const yearOfEra = adjustedYear - era * 400;
  const monthPrime = month + (month > 2 ? -3 : 9);
  const dayOfYear = Math.floor((153 * monthPrime + 2) / 5) + day - 1;
  const dayOfEra = yearOfEra * 365 + Math.floor(yearOfEra / 4) - Math.floor(yearOfEra / 100) + dayOfYear;
  return era * 146_097 + dayOfEra - 719_468;
}

function validationDuration(value: unknown, path: string): ValidationNumber {
  if (typeof value !== "string") invalid(path, "validation duration is invalid");
  const match = /^(-?)P(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)(?:\.(\d{1,9}))?S)?)?$/.exec(value);
  if (match === null) invalid(path, "validation duration is invalid");
  const sign = match[1] === "-" ? -1n : 1n;
  const seconds = BigInt(match[2] ?? "0") * 86_400n + BigInt(match[3] ?? "0") * 3_600n + BigInt(match[4] ?? "0") * 60n + BigInt(match[5] ?? "0");
  const nanoseconds = BigInt((match[6] ?? "").padEnd(9, "0") || "0");
  return { numerator: sign * (seconds * 1_000_000_000n + nanoseconds), denominator: 1n };
}

function validationString(value: unknown, path: string): string {
  if (typeof value === "string") return value;
  if (typeof value === "boolean") return value ? "true" : "false";
  if (isValidationNumber(value) && value.denominator === 1n) return value.numerator.toString(10);
  invalid(path, "validation template value cannot convert to string");
}

function validationFormat(format: string, values: readonly unknown[], path: string): string {
  let index = 0;
  return format.replace(/%%|%[sdvq]/g, (token) => {
    if (token === "%%") return "%";
    if (index >= values.length) invalid(path, "validation format has too few arguments");
    const value = values[index++];
    if (token === "%q") return JSON.stringify(validationString(value, path));
    if (token === "%d") {
      const number = requireValidationNumber(value, path);
      if (number.denominator !== 1n) invalid(path, "validation format %d requires an integer");
      return number.numerator.toString(10);
    }
    if (token === "%s") return validationString(value, path);
    if (typeof value === "string" || typeof value === "boolean") return String(value);
    if (isValidationNumber(value)) return value.denominator === 1n ? value.numerator.toString(10) : §${value.numerator}/${value.denominator}§;
    return JSON.stringify(value);
  });
}
`
}
