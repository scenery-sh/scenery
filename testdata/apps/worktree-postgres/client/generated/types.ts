// Code generated for the application. DO NOT EDIT.

declare const jsonNumberBrand: unique symbol;
export type JsonValue = null | boolean | string | JsonNumber | readonly JsonValue[] | { readonly [key: string]: JsonValue };
export interface JsonNumber { readonly coefficient: string; readonly scale: number; readonly [jsonNumberBrand]: true }
export type Unit = Readonly<Record<never, never>>;
export type UUIDString = string & { readonly __uuid: unique symbol };
export type DateString = string & { readonly __date: unique symbol };
export type DateTimeString = string & { readonly __datetime: unique symbol };
export type DurationString = string & { readonly __duration: unique symbol };
export type DecimalString = string & { readonly __decimal: unique symbol };
export type URLString = string & { readonly __url: unique symbol };
export type RelativePathString = string & { readonly __relativePath: unique symbol };
export interface Problem { readonly code: string; readonly message: string; readonly path?: string }
export interface EnqueueReceipt { readonly durableIdentity: string; readonly executionId: string; readonly acceptedRevision: string; readonly statusUrl?: URLString }

export interface Book {
  readonly bookId: string;
  readonly borrower: string;
  readonly title: string;
}

export interface BookList {
  readonly books: readonly Book[];
}

export interface BorrowInput {
  readonly bookId: string;
  readonly loan: Loan;
}

export interface CreateInput {
  readonly bookId: string;
  readonly title: string;
}

export interface Loan {
  readonly borrower: string;
}

export interface Lookup {
  readonly bookId: string;
}

type _FailureSet0 =
  | { readonly kind: "failure"; readonly name: "invalid_request"; readonly problem: Problem }
  | { readonly kind: "failure"; readonly name: "not_acceptable"; readonly problem: Problem }
  | { readonly kind: "failure"; readonly name: "rate_limited"; readonly problem: Problem }
;

type _FailureSet1 =
  | { readonly kind: "failure"; readonly name: "invalid_request"; readonly problem: Problem }
  | { readonly kind: "failure"; readonly name: "not_acceptable"; readonly problem: Problem }
  | { readonly kind: "failure"; readonly name: "rate_limited"; readonly problem: Problem }
  | { readonly kind: "failure"; readonly name: "unsupported_media_type"; readonly problem: Problem }
;

export type BorrowOutcome =
  | { readonly kind: "result"; readonly name: "borrowed"; readonly value: Book }
  | { readonly kind: "result"; readonly name: "conflict"; readonly value: Lookup }
  | { readonly kind: "result"; readonly name: "missing"; readonly value: Lookup }
  | _FailureSet1
;

export type CreateOutcome =
  | { readonly kind: "result"; readonly name: "conflict"; readonly value: Lookup }
  | { readonly kind: "result"; readonly name: "created"; readonly value: Book }
  | _FailureSet1
;

export type ListInput = Unit;

export type ListOutcome =
  | { readonly kind: "result"; readonly name: "listed"; readonly value: BookList }
  | _FailureSet0
;

export type ReturnInput = Lookup;

export type ReturnOutcome =
  | { readonly kind: "result"; readonly name: "conflict"; readonly value: Lookup }
  | { readonly kind: "result"; readonly name: "missing"; readonly value: Lookup }
  | { readonly kind: "result"; readonly name: "returned"; readonly value: Book }
  | _FailureSet0
;
