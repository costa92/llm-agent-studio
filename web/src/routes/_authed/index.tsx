import { useState } from "react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useMutation } from "@tanstack/react-query"
import { toast } from "sonner"
import { apiJSON } from "@/lib/apiClient"
import { Button } from "@/components/studio/Button"
import { Input } from "@/components/ui/input"

// 根落地（已认证）：无 org-list 端点可派生默认 org（org slug 来自 URL，所有视图按 /orgs/$org 寻址）。
// 故提供可操作落地：输入已有 org 进入，或新建 org（POST /api/orgs → {id}，id 即路由 slug）。
// 二者皆导航到 /orgs/{org}/projects，避免用户停滞在静态占位上。
export const Route = createFileRoute("/_authed/")({
  component: HomeLanding,
})

function HomeLanding() {
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
    const org = orgInput.trim()
    if (!org) return
    void navigate({ to: "/orgs/$org/projects", params: { org } })
  }

  return (
    <div className="grid h-full place-items-center">
      <div className="flex w-[min(420px,90vw)] flex-col gap-6">
        <header className="flex flex-col gap-1.5 text-center">
          <h1 className="font-heading text-[18px] font-bold text-text-1">进入组织</h1>
          <p className="text-[12.5px] text-text-3">
            输入你的组织标识进入，或新建一个组织。
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
          <div className="flex gap-2">
            <Input
              id="org-slug"
              value={orgInput}
              onChange={(e) => setOrgInput(e.target.value)}
              placeholder="如 acme"
              autoComplete="off"
            />
            <Button type="submit" variant="amber" disabled={orgInput.trim() === ""}>
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
          <div className="flex gap-2">
            <Input
              id="org-name"
              value={newOrgName}
              onChange={(e) => setNewOrgName(e.target.value)}
              placeholder="组织名称"
              autoComplete="off"
            />
            <Button type="submit" disabled={newOrgName.trim() === "" || create.isPending}>
              {create.isPending ? "创建中…" : "创建"}
            </Button>
          </div>
        </form>
      </div>
    </div>
  )
}
