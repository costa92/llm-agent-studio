import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { Plus, ChevronRight, Building2 } from "lucide-react"
import { apiJSON } from "@/lib/apiClient"
import { Button } from "@/components/studio/Button"
import { Input } from "@/components/ui/input"

interface OrgItem {
  id: string
  name: string
  role: string
}

export function OrgLanding() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const [newOrgName, setNewOrgName] = useState("")

  // 列出当前用户所属组织（GET /api/orgs → {items:[{id,name,role}]}）。登录响应
  // 不含组织信息，故此处拉取可点选列表，避免用户停滞在 OrgLanding 上。
  const orgs = useQuery({
    queryKey: ["my-orgs"],
    queryFn: () => apiJSON<{ items: OrgItem[] }>("/api/orgs").then((e) => e.items),
  })

  const create = useMutation({
    mutationFn: (name: string) =>
      apiJSON<{ id: string; name: string }>(`/api/orgs`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      }),
    onSuccess: (org) => {
      toast.success("组织已创建")
      void qc.invalidateQueries({ queryKey: ["my-orgs"] })
      void navigate({ to: "/orgs/$org/projects", params: { org: org.id } })
    },
    onError: () => toast.error("创建组织失败，请重试"),
  })

  function enter(org: OrgItem): void {
    void navigate({ to: "/orgs/$org/projects", params: { org: org.id } })
  }

  const items = orgs.data ?? []

  return (
    <div className="grid h-full min-h-[520px] place-items-center px-6">
      <div className="flex w-[min(460px,100%)] flex-col gap-6">
        <header className="flex flex-col gap-1.5 text-center">
          <h1 className="font-heading text-[20px] font-bold text-text-1">进入组织</h1>
          <p className="text-[12.5px] text-text-3">选择一个组织继续工作，或创建新的组织。</p>
        </header>

        {/* 组织列表 */}
        <section className="flex flex-col gap-2">
          <span className="text-[11px] font-semibold tracking-[0.08em] text-text-3">我的组织</span>
          {orgs.isLoading ? (
            <p className="rounded-md border border-line px-3 py-4 text-center text-[12.5px] text-text-3">
              加载中…
            </p>
          ) : orgs.isError ? (
            <p className="rounded-md border border-line px-3 py-4 text-center text-[12.5px] text-danger">
              加载组织失败，请刷新重试。
            </p>
          ) : items.length === 0 ? (
            <p className="rounded-md border border-dashed border-line px-3 py-4 text-center text-[12.5px] text-text-3">
              还没有组织，在下方创建一个开始。
            </p>
          ) : (
            <ul className="flex flex-col gap-1.5">
              {items.map((org) => (
                <li key={org.id}>
                  <button
                    type="button"
                    onClick={() => enter(org)}
                    className="group flex w-full items-center gap-3 rounded-md border border-line px-3 py-2.5 text-left transition-[0.15s] hover:border-amber hover:bg-surface-2"
                  >
                    <span className="grid h-8 w-8 place-items-center rounded-md bg-surface-2 text-text-2">
                      <Building2 className="h-4 w-4" />
                    </span>
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate text-[13px] font-medium text-text-1">{org.name}</span>
                      <span className="text-[11px] text-text-3">{org.role}</span>
                    </span>
                    <ChevronRight className="h-4 w-4 text-text-3 transition-[0.15s] group-hover:text-amber" />
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>

        <div className="flex items-center gap-3 text-[11px] text-text-3">
          <span className="h-px flex-1 bg-line" />
          或
          <span className="h-px flex-1 bg-line" />
        </div>

        {/* 新建组织 */}
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
            <Button type="submit" variant="amber" disabled={newOrgName.trim() === "" || create.isPending}>
              <Plus className="mr-2 h-4 w-4" />
              {create.isPending ? "创建中" : "创建"}
            </Button>
          </div>
        </form>
      </div>
    </div>
  )
}
