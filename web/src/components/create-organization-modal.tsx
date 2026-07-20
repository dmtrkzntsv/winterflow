"use client"

import * as React from "react"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

interface CreateOrganizationModalProps {
  isOpen: boolean
  onClose: () => void
  onSuccess?: (newOrganization: OrganizationPreview) => void
}

export type OrganizationPreview = {
  id: string
  name: string
}

export function CreateOrganizationModal({ 
  isOpen, 
  onClose,
  onSuccess
}: CreateOrganizationModalProps) {
  const [name, setName] = React.useState("")
  const [isLoading, setIsLoading] = React.useState(false)
  const inputRef = React.useRef<HTMLInputElement | null>(null)

  const handleClose = () => {
    setName("")
    onClose()
  }

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()

    if (!name.trim()) {
      return
    }

    if (name.length > 255) {
      return
    }

    setIsLoading(true)

    const trimmedName = name.trim()
    const generatedId =
      typeof crypto !== "undefined" && typeof crypto.randomUUID === "function"
        ? crypto.randomUUID()
        : `org-${Date.now()}`

    setTimeout(() => {
      const newOrganization: OrganizationPreview = {
        id: generatedId,
        name: trimmedName,
      }
      if (onSuccess) {
        onSuccess(newOrganization)
      }

      handleClose()
      setIsLoading(false)
    }, 800)
  }

  React.useEffect(() => {
    if (isOpen) {
      // Defer to ensure element is mounted
      setTimeout(() => inputRef.current?.focus(), 0)
    }
  }, [isOpen])

  return (
    <Dialog 
      open={isOpen} 
      onOpenChange={(open) => { if (!open && !isLoading) handleClose() }}
    >
      <DialogContent 
        className="sm:max-w-[425px] animate-none transition-none transform-none !duration-0"
        style={{ animationDuration: '0s', transitionDuration: '0s' }}
        onEscapeKeyDown={(e) => { if (isLoading) e.preventDefault() }}
        onPointerDownOutside={(e) => { if (isLoading) e.preventDefault() }}
      >
        <DialogHeader>
          <DialogTitle>Create New Organization</DialogTitle>
          <DialogDescription>
            Create a new organization to group your servers.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit}>
          <div className="grid gap-4 py-4">
            <div className="grid grid-cols-4 items-center gap-4">
              <Label htmlFor="name" className="text-right">
                Name
              </Label>
              <Input
                id="name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Enter organization name"
                className="col-span-3"
                maxLength={255}
                disabled={isLoading}
                ref={inputRef}
                autoFocus
                required
              />
            </div>
          </div>
          <DialogFooter>
            <Button 
              type="button" 
              variant="outline" 
              onClick={handleClose}
              disabled={isLoading}
              className="cursor-pointer"
            >
              Cancel
            </Button>
            <Button 
              type="submit" 
              disabled={!name.trim() || isLoading}
              className="cursor-pointer"
            >
              {isLoading ? "Creating..." : "Create Organization"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
