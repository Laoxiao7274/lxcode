// model.list → 菜单投影的**纯函数**（不碰 Android / Compose / 网络）。
//
// 抽出来的理由：这段映射的口径（组合键主键、显示名、选中态比较）是与后端
// `config.ModelConfig.Key()`、桌面端 `shared/settings-models.ts` 的契约，错一个
// 字就是「填了 provider 标签后选中态不高亮、药丸显示 `provider::id`」这类用户
// 可见的 bug——放纯函数里才能用 JVM 单测钉住（工程约定「构建即验证」）。
package com.moyunteng.lxcode.remote.model

import org.json.JSONObject

/** model.list 的投影结果：按端点分组的模型 + default 角色绑定（注册表主键）。 */
data class ModelListSnapshot(
    val groups: List<ModelGroup>,
    val defaultModelId: String,
)

/**
 * 把 `model.list` 的应答投影成菜单数据。
 *
 * 口径（与桌面端 `mapModels` 逐字对齐）：
 *  * `ModelMeta.id` = **注册表主键** `modelKey(provider, id)`——选中态比较、`chat.model`
 *    的 `model_id`、`chat.history.model` / `chat.modelChanged.model_id` 全是它；
 *  * `ModelMeta.modelId` = 条目自己的 model id（**纯展示**）；
 *  * `name` 回落顺序 = `display_name` → `model` → 纯 id；
 *  * 只收 `enabled` 的条目（禁用的模型不该出现在会话的切换菜单里）。
 */
fun parseModelList(r: JSONObject): ModelListSnapshot {
    val models = r.optJSONArray("models") ?: return ModelListSnapshot(emptyList(), "")
    val groups = linkedMapOf<String, MutableList<ModelMeta>>()
    for (i in 0 until models.length()) {
        val m = models.optJSONObject(i) ?: continue
        if (!m.optBoolean("enabled")) continue
        val rawId = m.optString("id")
        if (rawId.isEmpty()) continue
        val provider = m.optString("provider", "")
        val baseUrl = m.optString("base_url")
        val meta = ModelMeta(
            id = modelKey(provider, rawId),
            modelId = rawId,
            provider = provider,
            name = m.optString("display_name").ifBlank { m.optString("model", rawId) },
            desc = if (m.optString("format") == "anthropic") "Anthropic 格式" else "OpenAI 兼容",
            // 档位派生自能力声明：未声明 reasoning 的模型整个强度入口隐藏
            //（对非推理端点传 effort 参数会 400）
            efforts = if (m.optJSONObject("capabilities")?.optBoolean("reasoning") == true) {
                EFFORT_IDS
            } else {
                emptyList()
            },
        )
        groups.getOrPut(baseUrl) { mutableListOf() }.add(meta)
    }
    val grouped = groups.map { (url, ms) -> ModelGroup(name = hostOf(url), models = ms) }
    // roles.default 落的是**规范键**（组合键；存量纯 id）——与 ModelMeta.id 同口径
    val defaultModelId = r.optJSONObject("roles")?.optString("default").orEmpty()
    return ModelListSnapshot(grouped, defaultModelId)
}

/** 端点 → 组名（host；解析不出来就原样用 URL，坏配置也能显示供用户修）。 */
fun hostOf(baseUrl: String): String = try {
    java.net.URI("https://" + baseUrl.substringAfter("://")).host ?: baseUrl
} catch (_: Exception) {
    baseUrl
}

/**
 * 模型药丸/菜单上显示的**名字**。
 *
 * 注册表里查得到就用条目的 name（`display_name`/`model`/纯 id 的回落）；查不到
 * （刚切会话、注册表还没拉到、老后端）时把选中键剥成纯 id（`modelIdOfKey`）——
 * **绝不**把 `provider::id` 这种组合键原样显示给用户。全空 = "模型"。
 */
fun modelDisplayLabel(models: List<ModelMeta>, activeId: String): String {
    if (activeId.isEmpty()) return "模型"
    models.firstOrNull { it.id == activeId }?.let { if (it.name.isNotBlank()) return it.name }
    return modelIdOfKey(activeId).ifBlank { "模型" }
}
