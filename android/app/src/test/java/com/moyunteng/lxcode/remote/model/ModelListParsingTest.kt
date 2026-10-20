// 模型注册表投影的纯 JVM 单测（工程约定「构建即验证」，不引 robolectric）：
//   * 组合键拼接：`provider::id`（对齐后端 config.ModelConfig.Key() 与桌面端
//     shared/settings-models.ts 的 modelKey）——选中态比较与 chat.model 的
//     model_id 都用它；
//   * 无 provider（存量条目）回落纯 id；
//   * 显示名提取：菜单/药丸显示条目 name 或纯 modelId，**绝不**显示组合键。
//
// 背景（用户报的现象）：安卓 applyModelList 原先只读纯 `id`，后端 ModelID() 改成
// 组合键之后，填了 provider 标签的模型选中态不高亮、药丸显示 `openai::gpt-4o`。
package com.moyunteng.lxcode.remote.model

import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ModelListParsingTest {

    /** 一条注册表条目的 wire 形状（对齐 config.ModelConfig 的 JSON 标签）。 */
    private fun entry(
        id: String,
        provider: String? = null,
        displayName: String = "",
        model: String = "",
        baseUrl: String = "https://api.example.com/v1",
        enabled: Boolean = true,
        reasoning: Boolean = false,
    ): JSONObject {
        val o = JSONObject()
            .put("id", id)
            .put("base_url", baseUrl)
            .put("model", model.ifEmpty { id })
            .put("enabled", enabled)
        if (provider != null) o.put("provider", provider)
        if (displayName.isNotEmpty()) o.put("display_name", displayName)
        o.put("capabilities", JSONObject().put("reasoning", reasoning))
        return o
    }

    private fun list(vararg entries: JSONObject, defaultRole: String = ""): JSONObject {
        val arr = JSONArray()
        entries.forEach { arr.put(it) }
        val roles = JSONObject()
        if (defaultRole.isNotEmpty()) roles.put("default", defaultRole)
        return JSONObject().put("models", arr).put("roles", roles)
    }

    // ===== 组合键拼接 =====

    @Test
    fun providerTaggedModelGetsCompositeKey() {
        val snap = parseModelList(list(entry(id = "gpt-4o", provider = "openai")))
        val meta = snap.groups.single().models.single()
        // 主键 = provider::id（与后端 Key() / 桌面端 modelKey 逐字一致）
        assertEquals("openai::gpt-4o", meta.id)
        // 条目自己的 id 单独存（纯展示）
        assertEquals("gpt-4o", meta.modelId)
        assertEquals("openai", meta.provider)
    }

    @Test
    fun modelWithoutProviderFallsBackToPlainId() {
        val snap = parseModelList(list(entry(id = "deepseek-v3")))
        val meta = snap.groups.single().models.single()
        // 存量条目零迁移：主键就是纯 id
        assertEquals("deepseek-v3", meta.id)
        assertEquals("deepseek-v3", meta.modelId)
        assertEquals("", meta.provider)
    }

    @Test
    fun sameIdUnderTwoProvidersCoexist() {
        // 两家提供商的同名模型必须并存（这正是引入组合键的理由）
        val snap = parseModelList(
            list(
                entry(id = "gpt-4o", provider = "openai", baseUrl = "https://a.example.com/v1"),
                entry(id = "gpt-4o", provider = "azure", baseUrl = "https://b.example.com/v1"),
            ),
        )
        val ids = snap.groups.flatMap { it.models }.map { it.id }.toSet()
        assertEquals(setOf("openai::gpt-4o", "azure::gpt-4o"), ids)
    }

    @Test
    fun defaultRoleKeyComparesWithCompositeKey() {
        // roles.default 落的是规范键（组合键）——选中态比较必须能逐字命中
        val snap = parseModelList(
            list(entry(id = "gpt-4o", provider = "openai"), defaultRole = "openai::gpt-4o"),
        )
        val meta = snap.groups.single().models.single()
        assertEquals(snap.defaultModelId, meta.id)
    }

    @Test
    fun defaultRolePlainIdStillMatchesLegacyEntry() {
        val snap = parseModelList(list(entry(id = "deepseek-v3"), defaultRole = "deepseek-v3"))
        assertEquals("deepseek-v3", snap.defaultModelId)
        assertEquals(snap.defaultModelId, snap.groups.single().models.single().id)
    }

    // ===== 显示名提取 =====

    @Test
    fun displayNamePrefersRegistryName() {
        val snap = parseModelList(
            list(entry(id = "gpt-4o", provider = "openai", displayName = "GPT-4o")),
        )
        val models = snap.groups.flatMap { it.models }
        assertEquals("GPT-4o", modelDisplayLabel(models, "openai::gpt-4o"))
    }

    @Test
    fun displayNameNeverShowsCompositeKey() {
        // 注册表还没拉到（查不到条目）：药丸必须显示纯 id，不许出现 provider::
        assertEquals("gpt-4o", modelDisplayLabel(emptyList(), "openai::gpt-4o"))
    }

    @Test
    fun displayNameFallsBackToModelFieldThenId() {
        val snap = parseModelList(list(entry(id = "m1", model = "gpt-4o-mini")))
        val models = snap.groups.flatMap { it.models }
        // display_name 空 → model 字段
        assertEquals("gpt-4o-mini", models.single().name)
        // 都空 → 纯 id
        val snap2 = parseModelList(list(entry(id = "m2", model = "")))
        assertEquals("m2", snap2.groups.flatMap { it.models }.single().name)
    }

    @Test
    fun emptySelectionShowsPlaceholder() {
        assertEquals("模型", modelDisplayLabel(emptyList(), ""))
    }

    // ===== 过滤与分组 =====

    @Test
    fun disabledModelsAreExcluded() {
        val snap = parseModelList(
            list(
                entry(id = "on", provider = "p"),
                entry(id = "off", provider = "p", enabled = false),
            ),
        )
        val ids = snap.groups.flatMap { it.models }.map { it.id }
        assertEquals(listOf("p::on"), ids)
    }

    @Test
    fun groupsSplitByEndpointHost() {
        val snap = parseModelList(
            list(
                entry(id = "a", baseUrl = "https://api.one.com/v1"),
                entry(id = "b", baseUrl = "https://api.two.com/v1"),
            ),
        )
        assertEquals(setOf("api.one.com", "api.two.com"), snap.groups.map { it.name }.toSet())
    }

    @Test
    fun reasoningCapabilityDrivesEfforts() {
        val snap = parseModelList(
            list(
                entry(id = "r", reasoning = true),
                entry(id = "plain", reasoning = false),
            ),
        )
        val models = snap.groups.flatMap { it.models }
        assertEquals(EFFORT_IDS, models.first { it.id == "r" }.efforts)
        assertTrue(models.first { it.id == "plain" }.efforts.isEmpty())
    }

    // ===== 键工具函数本身 =====

    @Test
    fun modelKeyMatchesBackendConvention() {
        assertEquals("openai::gpt-4o", modelKey("openai", "gpt-4o"))
        assertEquals("gpt-4o", modelKey("", "gpt-4o"))
        assertEquals("gpt-4o", modelKey(null, "gpt-4o"))
    }

    @Test
    fun modelIdOfKeyStripsProviderPrefix() {
        assertEquals("gpt-4o", modelIdOfKey("openai::gpt-4o"))
        // 存量键（没有分隔符）原样返回
        assertEquals("deepseek-v3", modelIdOfKey("deepseek-v3"))
        // 空串不被当成「前缀为空」
        assertEquals("", modelIdOfKey(""))
    }

    @Test
    fun providerIsNotConfusedWithIdContainingSeparator() {
        // 后端 validate 拒绝含 :: 的 id/provider——这里只钉住拆分口径不吞掉尾段
        assertEquals("a::b::c", modelKey("a", "b::c"))
        assertEquals("b::c", modelIdOfKey("a::b::c"))
        assertFalse(modelKey("a", "b::c").isEmpty())
    }
}
