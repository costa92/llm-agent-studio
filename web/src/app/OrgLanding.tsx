import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useMutation } from "@tanstack/react-query"
import { toast } from "sonner"
import { Plus, LogIn } from "lucide-react"
import { apiJSON } from "@/lib/apiClient"
import { Button } from "@/components/studio/Button"
import { Input } from "@/components/ui/input"
import { cleanOrg } from "./org"

export function OrgLanding() {
  const navigate = useNavigate()
  const [orgInput, setOrgInput] = useState("")
  const [newOrgName, setNewOrgName] = useState("")

  const create = useMutation({
    mutationFn: (name: string) =>
      apiJSON<{ id: string; name: string }>(`/api/orgs`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      }),
    onSuccess: (org) => {
      toast.success("组织已创建")
      void navigate({ to: "/orgs/$org/projects", params: { org: org.id } })
    },
    onError: () => toast.error("创建组织失败，请重试"),
  })

  function enterOrg(): void {
    const org = cleanOrg(orgInput)
    if (org === "") return
    void navigate({ to: "/orgs/$org/projects", params: { org } })
  }

  return (
    <div className="grid h-full min-h-[520px] place-items-center px-6">
      <div className="flex w-[min(460px,100%)] flex-col gap-6">
        <header className="flex flex-col gap-1.5 text-center">
          <h1 className="font-heading text-[20px] font-bold text-text-1">进入组织</h1>
          <p className="text-[12.5px] text-text-3">
            选择一个组织标识继续工作，或创建新的组织。
          </p>
        </header>

        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            enterOrg()
          }}
        >
          <label className="text-[11px] font-semibold tracking-[0.08em] text-text-3" htmlFor="org-slug">
            组织标识
          </label>
          <div className="grid grid-cols-[1fr_auto] gap-2">
            <Input
              id="org-slug"
              value={orgInput}
              onChange={(e) => setOrgInput(e.target.value)}
              placeholder="如 acme"
              autoComplete="off"
            />
            <Button type="submit" variant="amber" disabled={cleanOrg(orgInput) === ""}>
              <LogIn className="mr-2 h-4 w-4" />
              进入
            </Button>
          </div>
        </form>

        <div className="flex items-center gap-3 text-[11px] text-text-3">
          <span className="h-px flex-1 bg-line" />
          或
          <span className="h-px flex-1 bg-line" />
        </div>

        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            const name = newOrgName.trim()
            if (name) create.mutate(name)
          }}
        >
          <label className="text-[11px] font-semibold tracking-[0.08em] text-text-3" htmlFor="org-name">
            新建组织
          </label>
          <div className="grid grid-cols-[1fr_auto] gap-2">
            <Input
              id="org-name"
              value={newOrgName}
              onChange={(e) => setNewOrgName(e.target.value)}
              placeholder="组织名称"
              autoComplete="off"
            />
            <Button type="submit" disabled={newOrgName.trim() === "" || create.isPending}>
              <Plus className="mr-2 h-4 w-4" />
              {create.isPending ? "创建中" : "创建"}
            </Button>
          </div>
        </form>
      </div>
    </div>
  )
}
