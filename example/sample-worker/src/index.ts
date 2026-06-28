import { DurableObject } from "cloudflare:workers"
import { z } from "zod"

export default {
  async fetch(request, env) {
    const url = new URL(request.url)
    if (url.pathname !== "/gateway") {
      return new Response("Not Found", { status: 404 })
    }
    const id = env.CONDUIT_DO.idFromName("bot")
    return env.CONDUIT_DO.get(id).fetch(request)
  },
} satisfies ExportedHandler<Env>

const decoder = new TextDecoder()

const zInitMessage = z.object({
  type: z.literal("init"),
  token: z.string(),
})

const zDiscordPayload = z.object({
  t: z.string().nullable(),
  d: z.record(z.string(), z.unknown()).nullable(),
})

// biome-ignore lint/complexity/noBannedTypes: Ignore banned types for DurableObjectState
type DOState = DurableObjectState<{}>
export class ConduitDO extends DurableObject {
  ctx: DOState
  private token?: string

  constructor(ctx: DOState, env: Env) {
    super(ctx, env)
    this.ctx = ctx
  }

  async fetch() {
    const webSocketPair = new WebSocketPair()
    const [client, server] = Object.values(webSocketPair)
    this.ctx.acceptWebSocket(server)
    return new Response(null, { status: 101, webSocket: client })
  }

  async webSocketMessage(_ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    const text = typeof message === "string" ? message : decoder.decode(message)
    const raw = JSON.parse(text) as unknown

    const init = zInitMessage.safeParse(raw)
    if (init.success) {
      this.token = init.data.token
      await this.ctx.storage.put("token", init.data.token)
      return
    }

    const payload = zDiscordPayload.parse(raw)
    if (payload.t === "MESSAGE_CREATE") {
      const d = payload.d as { content?: string; channel_id?: string } | null
      if (d?.content === "!ping" && d.channel_id) {
        const token = this.token ?? await this.ctx.storage.get<string>("token")
        if (!token) return
        await fetch(
          `https://discord.com/api/v10/channels/${d.channel_id}/messages`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json", Authorization: token },
            body: JSON.stringify({ content: "Pong!" }),
          }
        )
      }
    }
  }
}
