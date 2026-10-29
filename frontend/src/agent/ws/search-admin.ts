// SearchAdminSource 域（网页搜索渠道——M4）：请求编排 + 缓存。
// applySearchChannels 双路：握手并行组的 search.channels.list 刷新（onInitList
// 注册）与 search.changed 事件的副作用（onEventSideEffect 注册——载荷就是快照，
// 直接采用，不必再往返一次 list：后端广播时已经把它算好了）。
import type { SearchAdminSource, SearchChannel, SearchChannelsSnapshot, SearchTestResult } from "../../shared/types";
import type { WsConnection } from "./connection";

export class SearchAdmin implements SearchAdminSource {
  /** 搜索渠道快照（search.changed 驱动刷新；设置面板网页搜索区的数据源）。 */
  private searchCache: SearchChannelsSnapshot = { channels: [], ready: false };
  private listeners = new Set<() => void>();

  constructor(private readonly conn: WsConnection) {
    conn.onInitList("search.channels.list", (r) => this.applySearchChannels(r));
    conn.onEventSideEffect((method, params) => {
      if (method !== "search.changed") return;
      this.applySearchChannels(params);
    });
  }

  channels(): SearchChannelsSnapshot {
    return this.searchCache;
  }

  onChannelsChanged(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  saveChannel(
    id: string,
    patch: { apiKey?: string; baseUrl?: string; options?: Record<string, string>; enabled?: boolean },
  ): Promise<void> {
    // 未给的字段按现值回填：后端是 upsert 语义（缺省即清空），
    // 不回填的话「只改 base_url」会把已存的 key 抹掉。
    const cur = this.searchCache.channels.find((c) => c.id === id);
    return this.conn.call("search.channel.save", {
      id,
      api_key: patch.apiKey ?? cur?.api_key ?? "",
      base_url: patch.baseUrl ?? cur?.base_url ?? "",
      // options 同样按现值回填：它是整体覆盖而非增量合并，
      // 不回填的话「只改 key」会把已存的 zone/档位抹掉。
      options: patch.options ?? cur?.options ?? {},
      enabled: patch.enabled ?? cur?.enabled ?? true,
    }).then(() => undefined);
  }

  removeChannel(id: string): Promise<void> {
    return this.conn.call("search.channel.remove", { id }).then(() => undefined);
  }

  setPrimary(id: string): Promise<void> {
    return this.conn.call("search.primary.set", { id }).then(() => undefined);
  }

  testChannel(id: string, query?: string): Promise<SearchTestResult> {
    return this.conn.call("search.test", { id, query: query ?? "" }) as Promise<SearchTestResult>;
  }

  /** search.channels.list 结果 / search.changed 载荷 → 缓存 + 通知。 */
  private applySearchChannels(result: unknown) {
    const r = result as Partial<SearchChannelsSnapshot> | null;
    if (!r || !Array.isArray(r.channels)) return;
    this.searchCache = {
      channels: r.channels as SearchChannel[],
      primary: r.primary,
      ready: r.ready === true,
    };
    this.listeners.forEach((l) => l());
  }
}
