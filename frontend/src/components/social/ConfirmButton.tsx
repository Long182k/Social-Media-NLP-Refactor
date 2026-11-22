import { useState } from 'react'
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { RequestError } from './Section'

export function ConfirmButton({ label, description, onConfirm }: {
  label: string; description: string; onConfirm: () => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<unknown>(null)

  async function confirm() {
    setPending(true)
    setError(null)
    try {
      await onConfirm()
      setOpen(false)
    } catch (err) {
      setError(err)
    } finally {
      setPending(false)
    }
  }
  return (
    <>
      <Button variant="outline" onClick={() => { setError(null); setOpen(true) }}>{label}</Button>
      <Dialog open={open} onOpenChange={(next) => { if (!pending) setOpen(next) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{label}</DialogTitle>
            <DialogDescription>{description}</DialogDescription>
          </DialogHeader>
          <RequestError error={error} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)} disabled={pending}>Cancel</Button>
            <Button variant="destructive" onClick={() => void confirm()} disabled={pending}>{label}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
