import { useState } from "react"
import { Eye, EyeOff } from "lucide-react"
import { Input } from "@/components/ui/input"
import { Button as UiButton } from "@/components/ui/button"

export interface RevealSecretInputProps {
  id?: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  alreadySet?: boolean
  onReveal?: () => Promise<string>
  disabled?: boolean
}

// 密钥输入：Eye 明文切换；alreadySet 提示「留空保持不变」；
// 传 onReveal 时多一个「显示已存密钥」按钮（异步解密回填）。不传则退化为普通 password。
export function RevealSecretInput({
  id, value, onChange, placeholder, alreadySet = false, onReveal, disabled = false,
}: RevealSecretInputProps) {
  const [show, setShow] = useState(false)
  const [revealing, setRevealing] = useState(false)
  const [revealError, setRevealError] = useState<string | null>(null)

  async function handleReveal() {
    if (!onReveal) return
    setRevealing(true)
    setRevealError(null)
    try {
      const v = await onReveal()
      onChange(v)
      setShow(true)
    } catch {
      setRevealError("无法读取已存密钥")
    } finally {
      setRevealing(false)
    }
  }

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-2">
        <Input
          id={id}
          aria-label="密钥输入"
          type={show ? "text" : "password"}
          value={value}
          placeholder={placeholder}
          disabled={disabled}
          onChange={(e) => onChange(e.target.value)}
        />
        <UiButton type="button" variant="ghost" size="sm"
          aria-label="显示/隐藏密钥" onClick={() => setShow((s) => !s)}>
          {show ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
        </UiButton>
        {onReveal && (
          <UiButton type="button" variant="outline" size="sm"
            aria-label="显示已存密钥" disabled={revealing} onClick={handleReveal}>
            {revealing ? "读取中…" : "显示已存"}
          </UiButton>
        )}
      </div>
      {alreadySet && (
        <p className="text-[11px] text-text-3">留空保持不变（已配置）；填写则替换为新值。</p>
      )}
      {revealError && <p className="text-[11px] text-red-400">{revealError}</p>}
    </div>
  )
}
