import { motion, Reorder, useDragControls } from 'framer-motion'
import { ArrowDown, ArrowUp, Check, GripVertical, RefreshCw, UserRound } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import { cn } from '@/lib/utils'
import type { Account, QuotaBalance } from '@/types/api'

interface AccountCardProps {
  account: Account
  isActive: boolean
  isFirst: boolean
  isLast: boolean
  refreshing: boolean
  onActivate: () => void
  onMoveUp: () => void
  onMoveDown: () => void
  onRefresh: () => void
  onDragEnd: () => void
}

function formatTokens(value: number | null): string {
  if (value == null) return '—'
  return new Intl.NumberFormat('pt-BR', { notation: 'compact', maximumFractionDigits: 1 }).format(value)
}

function quotaColor(percent: number): string {
  if (percent >= 90) return 'bg-red-500'
  if (percent >= 70) return 'bg-amber-400'
  return 'bg-emerald-500'
}

function ModelQuota({ balance }: { balance: QuotaBalance }) {
  const percent = Math.max(0, Math.min(100, balance.usagePercent ?? 0))
  return (
    <div className="rounded-md border border-border/50 bg-background/35 px-3 py-2.5">
      <div className="mb-2 flex items-center justify-between gap-3">
        <div>
          <p className="text-xs font-semibold">{balance.model}</p>
          <p className="text-[11px] text-muted-foreground">
            {formatTokens(balance.used)} usados de {formatTokens(balance.total)}
          </p>
        </div>
        <span className="text-xs font-semibold tabular-nums">{Math.round(percent)}%</span>
      </div>
      <Progress value={percent} indicatorClassName={quotaColor(percent)} className="h-1.5" />
      <div className="mt-1.5 flex justify-between text-[10px] text-muted-foreground">
        <span>{formatTokens(balance.available)} disponíveis</span>
        <span>{formatTokens(balance.remaining)} restantes</span>
      </div>
    </div>
  )
}

export function AccountCard({
  account,
  isActive,
  isFirst,
  isLast,
  refreshing,
  onActivate,
  onMoveUp,
  onMoveDown,
  onRefresh,
  onDragEnd,
}: AccountCardProps) {
  const dragControls = useDragControls()
  const displayName = account.user.name || account.label
  const email = account.user.email || account.user.id || account.id

  return (
    <Reorder.Item
      value={account}
      dragListener={false}
      dragControls={dragControls}
      onDragEnd={onDragEnd}
      layout
      whileDrag={{
        scale: 1.02,
        zIndex: 30,
        boxShadow: '0 22px 60px rgba(0,0,0,.32)',
      }}
      transition={{ type: 'spring', stiffness: 360, damping: 32 }}
      className="list-none"
    >
      <motion.article
        layout
        className={cn(
          'group relative overflow-hidden rounded-lg border bg-card/75',
          'transition-colors duration-200',
          isActive ? 'border-emerald-500/60 shadow-[0_8px_28px_rgba(16,185,129,.08)]' : 'border-border/70',
        )}
      >
        <div className="flex items-stretch">
          <button
            type="button"
            aria-label={`Arrastar ${account.label}`}
            title="Segure e arraste para reorganizar"
            onPointerDown={(event) => dragControls.start(event)}
            className="flex w-10 shrink-0 cursor-grab touch-none items-center justify-center border-r border-border/50 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground active:cursor-grabbing"
          >
            <GripVertical className="h-4 w-4" />
          </button>

          <div className="min-w-0 flex-1 p-4">
            <div className="flex items-start justify-between gap-4">
              <div className="flex min-w-0 items-center gap-3">
                <div className="relative h-10 w-10 shrink-0 overflow-hidden rounded-full border border-border/70 bg-muted">
                  {account.user.avatar ? (
                    <img src={account.user.avatar} alt="" className="h-full w-full object-cover" />
                  ) : (
                    <UserRound className="m-2.5 h-5 w-5 text-muted-foreground" />
                  )}
                </div>
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <h3 className="truncate text-sm font-semibold">{displayName}</h3>
                    <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                      {account.label}
                    </span>
                    {isActive && (
                      <span className="inline-flex items-center gap-1 rounded bg-emerald-500/12 px-1.5 py-0.5 text-[10px] font-semibold text-emerald-500">
                        <Check className="h-3 w-3" /> Em uso
                      </span>
                    )}
                  </div>
                  <p className="truncate text-xs text-muted-foreground">{email}</p>
                </div>
              </div>

              <div className="flex shrink-0 items-center gap-1">
                <Button variant="ghost" size="icon" title="Atualizar cota" onClick={onRefresh}>
                  <RefreshCw className={cn('h-4 w-4', refreshing && 'animate-spin')} />
                </Button>
                <Button variant="ghost" size="icon" title="Mover para cima" disabled={isFirst} onClick={onMoveUp}>
                  <ArrowUp className="h-4 w-4" />
                </Button>
                <Button variant="ghost" size="icon" title="Mover para baixo" disabled={isLast} onClick={onMoveDown}>
                  <ArrowDown className="h-4 w-4" />
                </Button>
                <Button
                  variant={isActive ? 'secondary' : 'outline'}
                  size="sm"
                  disabled={isActive}
                  onClick={onActivate}
                >
                  {isActive ? 'Conta ativa' : 'Usar agora'}
                </Button>
              </div>
            </div>

            {account.quota ? (
              <div className="mt-4 grid gap-2 sm:grid-cols-2">
                {account.quota.balances.map((balance) => (
                  <ModelQuota key={balance.id || balance.model} balance={balance} />
                ))}
              </div>
            ) : account.quotaError ? (
              <div className="mt-4 rounded-md border border-red-500/25 bg-red-500/5 px-3 py-2 text-xs text-red-400">
                Não foi possível consultar a cota: {account.quotaError.message}
              </div>
            ) : (
              <div className="mt-4 h-[74px] animate-pulse rounded-md bg-muted/60" />
            )}
          </div>
        </div>
      </motion.article>
    </Reorder.Item>
  )
}
