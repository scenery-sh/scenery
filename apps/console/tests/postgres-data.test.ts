import { expect, test } from 'bun:test'
import { postgresDataRows } from '../src/dashboard-utils'

test('PostgreSQL empty results retain an empty table', () => {
  expect(postgresDataRows(null)).toEqual([])
  expect(postgresDataRows({ columns: ['id'], rows: null, limit: 100, offset: 0 })).toEqual([])
  expect(postgresDataRows({ columns: ['id'], rows: [], limit: 100, offset: 0 })).toEqual([])
})

test('PostgreSQL populated results preserve columns and cell formatting', () => {
  expect(postgresDataRows({
    columns: ['id', 'optional', 'details'],
    rows: [[42, null, { enabled: true }]],
    limit: 100,
    offset: 0,
  })).toEqual([{ __id: '0', id: '42', optional: 'null', details: '{"enabled":true}' }])
})
