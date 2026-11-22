export interface SequenceState<T> {
  lastContiguousSeq: number
  items: Map<number, T>
  pending: Map<number, T>
}

export function emptySequence<T>(): SequenceState<T> {
  return { lastContiguousSeq: 0, items: new Map(), pending: new Map() }
}

export function mergeSequence<T extends { deleted?: boolean }>(
  previous: SequenceState<T>, incoming: T[], sequence: (item: T) => number,
): SequenceState<T> {
  const items = new Map(previous.items)
  const pending = new Map(previous.pending)
  let lastContiguousSeq = previous.lastContiguousSeq

  for (const item of incoming) {
    const seq = sequence(item)
    if (!Number.isSafeInteger(seq) || seq < 1) continue
    const target = seq <= lastContiguousSeq ? items : pending
    if (target.get(seq)?.deleted && !item.deleted) continue
    target.set(seq, item)
  }

  let expected = pending.get(lastContiguousSeq + 1)
  while (expected) {
    lastContiguousSeq += 1
    items.set(lastContiguousSeq, expected)
    pending.delete(lastContiguousSeq)
    expected = pending.get(lastContiguousSeq + 1)
  }
  return { lastContiguousSeq, items, pending }
}
