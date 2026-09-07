import { PublicApiClient } from './generated/client.js';
import type { URLString } from './generated/types.js';

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

export async function verify(baseUrl: string, bookId: string, phase: 'exercise' | 'persisted') {
  const statuses: number[] = [];
  const client = new PublicApiClient({
    baseUrl: baseUrl as URLString,
    fetch: async (input, init) => {
      const response = await fetch(input, init);
      statuses.push(response.status);
      return response;
    },
  });
  if (phase === 'persisted') {
    const result = await client.list({});
    assert(result.kind === 'result' && result.name === 'listed', 'list after restart');
    const book = result.value.books.find(book => book.bookId === bookId);
    assert(book?.title === 'The Left Hand of Darkness' && book.borrower === 'Petr', 'persisted title and borrower');
    console.log(JSON.stringify({ phase, book, statuses, ok: true }));
    return;
  }

  const created = await client.create({ bookId, title: 'The Left Hand of Darkness' });
  assert(created.kind === 'result' && created.name === 'created' && created.value.borrower === '', 'create available book');
  assert(statuses.at(-1) === 201, 'create status');
  const duplicate = await client.create({ bookId, title: 'Must not replace the title' });
  assert(duplicate.name === 'conflict' && statuses.at(-1) === 409, 'duplicate conflict');
  const missingId = `${bookId}-missing`;
  assert((await client.borrow({ bookId: missingId, loan: { borrower: 'Nobody' } })).name === 'missing', 'missing borrow');
  assert(statuses.at(-1) === 404, 'missing status');
  assert((await client.return({ bookId: missingId })).name === 'missing', 'missing return');
  assert((await client.return({ bookId })).name === 'conflict', 'return available book');

  // This is a real HTTP/PostgreSQL boundary probe, not an ordinary Go unit test.
  for (let round = 0; round < 10; round++) {
    const results = await Promise.all(['Alice', 'Bob'].map(borrower => client.borrow({ bookId, loan: { borrower } })));
    assert(results.filter(result => result.name === 'borrowed').length === 1, `one winner in round ${round}`);
    assert(results.filter(result => result.name === 'conflict').length === 1, `one conflict in round ${round}`);
    const list = await client.list({});
    assert(list.kind === 'result' && list.name === 'listed', 'list result');
    const winner = results.find(result => result.kind === 'result' && result.name === 'borrowed');
    assert(winner?.kind === 'result' && winner.name === 'borrowed', 'winner outcome');
    assert(list.value.books.find(book => book.bookId === bookId)?.borrower === winner.value.borrower, 'stored winner');
    const returned = await client.return({ bookId });
    assert(returned.kind === 'result' && returned.name === 'returned' && returned.value.borrower === '', 'return clears borrower');
  }

  // Raw HTTP bypasses client-side validation to prove the runtime checks inputs.
  const invalid = await fetch(`${baseUrl}/books/${encodeURIComponent(bookId)}/borrow`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ borrower: '' }),
  });
  assert(invalid.status === 400, 'runtime rejects empty borrower');
  const invalidBody = await invalid.json();
  assert(invalidBody.code === 'transport.invalid_request', 'typed runtime validation problem');
  const borrowed = await client.borrow({ bookId, loan: { borrower: 'Petr' } });
  assert(borrowed.name === 'borrowed', 'loan saved for restart');
  console.log(JSON.stringify({ phase, bookId, concurrentRounds: 10, statuses, invalidStatus: invalid.status, ok: true }));
}
