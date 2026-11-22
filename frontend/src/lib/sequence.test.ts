import assert from 'node:assert/strict'
import { test } from 'vitest'
import { emptySequence, mergeSequence } from './sequence.ts'

type Entry = { id: string; seq: number; deleted?: boolean }
const seq = (entry: Entry) => entry.seq

test('catches up only through contiguous sequences and buffers live frames across gaps', () => {
  const item = { id: 'later', seq: 3 }
  const pending = mergeSequence(emptySequence<Entry>(), [item], seq)
  assert.equal(pending.lastContiguousSeq, 0)
  assert.equal(pending.pending.size, 1)
  const updated = mergeSequence(pending, [{ id: 'first', seq: 1 }, { id: 'second', seq: 2 }], seq)
  assert.equal(updated.lastContiguousSeq, 3)
  assert.deepEqual([...updated.items.values()].map(({ id }) => id), ['first', 'second', 'later'])
  assert.equal(updated.pending.size, 0)
})

test('a catch-up tombstone closes a gap and cannot be resurrected by an older live item', () => {
  const newest = { id: 'after', seq: 2 }
  const pending = mergeSequence(emptySequence<Entry>(), [newest], seq)
  const tombstone = { id: 'removed', seq: 1, deleted: true }
  const caughtUp = mergeSequence(pending, [tombstone], seq)
  const duplicate = mergeSequence(caughtUp, [{ id: 'removed', seq: 1 }, newest], seq)
  assert.equal(duplicate.lastContiguousSeq, 2)
  assert.equal(duplicate.items.get(1)?.deleted, true)
  assert.equal(duplicate.items.size, 2)
})
