// lxcode 设计 token · 颜色
//
// 唯一事实源 = 原型 `temp/android-redesign/index.html` 行 79-199 的 CSS 变量
//（三变体 A 纯净精修 / B 品牌紫 / C 陶土助理 × 三色温 light / dark / warm = 9 组合）。
// 每个组合都是完整 token 集，无回落依赖——切换任一维度不漏值。
// 任何一值都能在原型 CSS 里按 `.v-*[data-temp=*]` 找到逐字对应；改色先改原型，再同步到这里。
//
// 形态：[LxColorSet] 不可变数据类（同名字段保持页面层引用形状）×
// [LxVariant] / [LxTemp] 两个枚举，九个实例全量展开在 [LxColorSets]。
package com.moyunteng.lxcode.design.token

import androidx.compose.ui.graphics.Color

/** 变体（品牌气质轴）。 */
enum class LxVariant {
    /** A · 纯净精修（白底近黑字，accent = 墨黑本身） */
    A,

    /** B · 品牌紫（工作台气质，单一紫色信号色） */
    B,

    /** C · 陶土助理（纸感暖底 + 陶土 accent） */
    C,
}

/** 色温（明暗轴，与变体正交）。 */
enum class LxTemp {
    /** 亮色 */
    Light,

    /** 暗色 */
    Dark,

    /** 暖色（纸感底） */
    Warm,
}

/**
 * 一套完整的颜色 token（一个 variant × temp 组合一份）。
 *
 * 字段命名与旧 `LxColors` object 一致，页面层 `Lx.colors.Fg` 的引用形状不变；
 * 每个字段的 KDoc 标注它对应原型 CSS 的哪个变量。
 */
data class LxColorSet(
    /** 页面底色 —— CSS `--bg` */
    val Bg: Color,

    /** 侧栏/次级面板底色 —— CSS `--bg2` */
    val BgSide: Color,

    /** 卡片、浮层、输入框底色 —— CSS `--surface` */
    val Surface: Color,

    /** 主文字、主按钮底 —— CSS `--fg` */
    val Fg: Color,

    /** 次要文字（副标题、ghost 按钮文字） —— CSS `--fg2` */
    val FgMuted: Color,

    /** 三级文字（占位符、分组标题、空态文案） —— CSS `--fg3` */
    val FgFaint: Color,

    /** 发丝边框（卡片、分隔线） —— CSS `--line` */
    val Border: Color,

    /** 控件边框（输入框、描边次按钮） —— CSS `--line2` */
    val BorderStrong: Color,

    /** 极浅填充（列表行 hover、次级药丸底） —— CSS `--soft` */
    val BorderSoft: Color,

    /** 强调色（主按钮底、链接、选中态） —— CSS `--accent` */
    val Accent: Color,

    /** 强调色上的文字（主按钮文字色） —— CSS `--accent-ink` */
    val AccentInk: Color,

    /** 强调色浅底（选中行、强调药丸底） —— CSS `--accent-soft` */
    val AccentSoft: Color,

    /** 成功语义（只作语义，不做装饰） —— CSS `--ok` */
    val Success: Color,

    /** 危险语义（删除/拒绝/失败） —— CSS `--danger` */
    val Danger: Color,

    /** 警示琥珀（运行中） —— CSS `--warn` */
    val Amber: Color,

    /** 用户气泡底 —— CSS `--bubble` */
    val BubbleUser: Color,

    /** 终端/代码块底 —— CSS `--code` */
    val TermBg: Color,

    /** 终端正文 —— CSS `--code-fg` */
    val TermFg: Color,

    /** 终端次要文字（cwd、提示符说明）—— 原型未单列，取 [TermFg] 的低透明度派生 */
    val TermDim: Color,

    /** 代码/终端卡底（终端块头、代码块 header 底） —— 原型未单列，取 [TermBg] 低透明度叠加 [Bg] 派生 */
    val CodeBg: Color,

    /** focus 边框/焦点环 —— CSS `--ring`（原型为低透明度色，安卓取实色化用于边框） */
    val Ring: Color,

    /** 琥珀药丸文字（比 [Amber] 深一档保证浅底可读）—— 原型未单列，保留旧色板值 */
    val AmberInk: Color,

    /** 成功文字（比 [Success] 深一档，浅底上可读）—— 原型未单列，保留旧色板值 */
    val SuccessInk: Color,

    /** 成功药丸底 —— 旧色板派生（[Success] × alpha，对齐桌面 `.ag-pill.risk-low`） */
    val SuccessSoft: Color,

    /** 危险药丸底 —— 旧色板派生（[Danger] × alpha，对齐桌面 `.ag-pill.risk-high`） */
    val DangerSoft: Color,

    /** 琥珀药丸底 —— 旧色板派生（[Amber] × alpha） */
    val AmberSoft: Color,

    /** 涟漪/按压指示 —— 取 [Fg] 的低透明度（对齐桌面 hover 填充 6%~10%） */
    val Ripple: Color,

    /** hero 区渐变（工具色，[gradHeroColors] 拆分后供 Brush 使用） —— CSS `--grad-hero` */
    val GradHero: List<Color>,
) {
    /** 涟漪/按压指示的兼容别名（旧代码写 `Lx.colors.Ripple`）。 */
    val RippleColor: Color get() = Ripple

    /** [GradHero] 的起止色（160~165deg 线性渐变，角度由使用方决定）。 */
    val gradHeroColors: List<Color> get() = GradHero
}

/** 九个组合的常量表（变体 × 色温全展开，无回落）。 */
object LxColorSets {

    // ===== A · 纯净精修 =====

    /** A + 亮色（旧色板基线，逐值一致——页面零视觉变化的保底） */
    val ALight = LxColorSet(
        Bg = Color(0xFFFFFFFF),
        BgSide = Color(0xFFF9F9F9),
        Surface = Color(0xFFFFFFFF),
        Fg = Color(0xFF0D0D0D),
        FgMuted = Color(0xFF6E6E80),
        FgFaint = Color(0xFF8E8EA0),
        Border = Color(0xFFECECEC),
        BorderStrong = Color(0xFFE5E5E5),
        BorderSoft = Color(0xFFF4F4F5),
        Accent = Color(0xFF0D0D0D),
        AccentInk = Color(0xFFFFFFFF),
        AccentSoft = Color(0xFFF4F4F5),
        Success = Color(0xFF10A37F),
        Danger = Color(0xFFE02E2A),
        Amber = Color(0xFFD97706),
        BubbleUser = Color(0xFFF1F1F3),
        TermBg = Color(0xFF1A1A1A),
        TermFg = Color(0xFFD4D4D4),
        TermDim = Color(0xFF8E8E96),
        CodeBg = Color(0xFFF4F5F7),
        Ring = Color(0xFF0D0D0D),
        AmberInk = Color(0xFF9A6400),
        SuccessInk = Color(0xFF0F7A5C),
        SuccessSoft = Color(0xFF10A37F).copy(alpha = 0.10f),
        DangerSoft = Color(0xFFE02E2A).copy(alpha = 0.08f),
        AmberSoft = Color(0xFFD97706).copy(alpha = 0.12f),
        Ripple = Color(0x14000000),
        GradHero = listOf(Color(0xFFFFFFFF), Color(0xFFF7F7F8)),
    )

    /** A + 暗色（默认档） */
    val ADark = LxColorSet(
        Bg = Color(0xFF101013),
        BgSide = Color(0xFF0C0C0F),
        Surface = Color(0xFF17171B),
        Fg = Color(0xFFECECF0),
        FgMuted = Color(0xFF9A9AA6),
        FgFaint = Color(0xFF75757F),
        Border = Color(0xFF242429),
        BorderStrong = Color(0xFF2D2D33),
        BorderSoft = Color(0xFF1F1F24),
        Accent = Color(0xFFF2F2F4),
        AccentInk = Color(0xFF101013),
        AccentSoft = Color(0xFF202025),
        Success = Color(0xFF2FBF95),
        Danger = Color(0xFFEF5350),
        Amber = Color(0xFFE0A23C),
        BubbleUser = Color(0xFF1D1D22),
        TermBg = Color(0xFF0C0C0F),
        TermFg = Color(0xFFC9C9CF),
        TermDim = Color(0xFF8E8E96),
        CodeBg = Color(0xFF0C0C0F),
        Ring = Color(0xFFECECF0),
        AmberInk = Color(0xFFE0A23C),
        SuccessInk = Color(0xFF2FBF95),
        SuccessSoft = Color(0xFF2FBF95).copy(alpha = 0.16f),
        DangerSoft = Color(0xFFEF5350).copy(alpha = 0.14f),
        AmberSoft = Color(0xFFE0A23C).copy(alpha = 0.16f),
        Ripple = Color(0x1AECECF0),
        GradHero = listOf(Color(0xFF17171B), Color(0xFF101013)),
    )

    /** A + 暖色 */
    val AWarm = LxColorSet(
        Bg = Color(0xFFFAF6EF),
        BgSide = Color(0xFFF3EDE2),
        Surface = Color(0xFFFFFDF9),
        Fg = Color(0xFF302A24),
        FgMuted = Color(0xFF8A7D6F),
        FgFaint = Color(0xFFA89A8A),
        Border = Color(0xFFEAE1D3),
        BorderStrong = Color(0xFFE1D7C6),
        BorderSoft = Color(0xFFF3ECDF),
        Accent = Color(0xFF3A3128),
        AccentInk = Color(0xFFFAF6EF),
        AccentSoft = Color(0xFFEFE8DB),
        Success = Color(0xFF3F8F6A),
        Danger = Color(0xFFCF3F3C),
        Amber = Color(0xFFD97706),
        BubbleUser = Color(0xFFF3ECDF),
        TermBg = Color(0xFF2B251F),
        TermFg = Color(0xFFE8DFCF),
        TermDim = Color(0xFFA89A8A),
        CodeBg = Color(0xFFF3ECDF),
        Ring = Color(0xFF3A3128),
        AmberInk = Color(0xFF9A6400),
        SuccessInk = Color(0xFF356B52),
        SuccessSoft = Color(0xFF3F8F6A).copy(alpha = 0.10f),
        DangerSoft = Color(0xFFCF3F3C).copy(alpha = 0.08f),
        AmberSoft = Color(0xFFD97706).copy(alpha = 0.12f),
        Ripple = Color(0x14302A24),
        GradHero = listOf(Color(0xFFFFFDF9), Color(0xFFF5EFE4)),
    )

    // ===== B · 品牌紫 =====

    /** B + 亮色 */
    val BLight = LxColorSet(
        Bg = Color(0xFFFFFFFF),
        BgSide = Color(0xFFF8F8FB),
        Surface = Color(0xFFFFFFFF),
        Fg = Color(0xFF17171F),
        FgMuted = Color(0xFF6A6A7E),
        FgFaint = Color(0xFF8E8EA0),
        Border = Color(0xFFECECF1),
        BorderStrong = Color(0xFFE4E4EC),
        BorderSoft = Color(0xFFF4F4F9),
        Accent = Color(0xFF6D5AE0),
        AccentInk = Color(0xFFFFFFFF),
        AccentSoft = Color(0xFF6D5AE0).copy(alpha = 0.09f),
        Success = Color(0xFF10A37F),
        Danger = Color(0xFFE02E2A),
        Amber = Color(0xFFD97706),
        BubbleUser = Color(0xFFF2F1FA),
        TermBg = Color(0xFF1A1A22),
        TermFg = Color(0xFFD4D4DE),
        TermDim = Color(0xFF8E8EA0),
        CodeBg = Color(0xFFF4F4F9),
        Ring = Color(0xFF6D5AE0),
        AmberInk = Color(0xFF9A6400),
        SuccessInk = Color(0xFF0F7A5C),
        SuccessSoft = Color(0xFF10A37F).copy(alpha = 0.10f),
        DangerSoft = Color(0xFFE02E2A).copy(alpha = 0.08f),
        AmberSoft = Color(0xFFD97706).copy(alpha = 0.12f),
        Ripple = Color(0x1417171F),
        GradHero = listOf(Color(0xFFF4F2FD), Color(0xFFFFFFFF)),
    )

    /** B + 暗色（B 的本体形态） */
    val BDark = LxColorSet(
        Bg = Color(0xFF0B0B10),
        BgSide = Color(0xFF08080C),
        Surface = Color(0xFF14141C),
        Fg = Color(0xFFE9E9F2),
        FgMuted = Color(0xFF9D9DB2),
        FgFaint = Color(0xFF73738A),
        Border = Color(0xFF232330),
        BorderStrong = Color(0xFF2C2C3C),
        BorderSoft = Color(0xFF1B1B26),
        Accent = Color(0xFF8B7CF6),
        AccentInk = Color(0xFFFFFFFF),
        AccentSoft = Color(0xFF8B7CF6).copy(alpha = 0.14f),
        Success = Color(0xFF2FBF95),
        Danger = Color(0xFFEF5350),
        Amber = Color(0xFFE0A23C),
        BubbleUser = Color(0xFF1C1B28),
        TermBg = Color(0xFF0C0C12),
        TermFg = Color(0xFFC9C9DD),
        TermDim = Color(0xFF73738A),
        CodeBg = Color(0xFF0C0C12),
        Ring = Color(0xFF8B7CF6),
        AmberInk = Color(0xFFE0A23C),
        SuccessInk = Color(0xFF2FBF95),
        SuccessSoft = Color(0xFF2FBF95).copy(alpha = 0.16f),
        DangerSoft = Color(0xFFEF5350).copy(alpha = 0.14f),
        AmberSoft = Color(0xFFE0A23C).copy(alpha = 0.16f),
        Ripple = Color(0x1AE9E9F2),
        GradHero = listOf(Color(0xFF181629), Color(0xFF0B0B10)),
    )

    /** B + 暖色 */
    val BWarm = LxColorSet(
        Bg = Color(0xFFFAF6EF),
        BgSide = Color(0xFFF3EDE2),
        Surface = Color(0xFFFFFDF9),
        Fg = Color(0xFF2B2630),
        FgMuted = Color(0xFF857A80),
        FgFaint = Color(0xFFA39A9C),
        Border = Color(0xFFEAE1D8),
        BorderStrong = Color(0xFFE1D6D0),
        BorderSoft = Color(0xFFF3ECDF),
        Accent = Color(0xFF7B62D2),
        AccentInk = Color(0xFFFFFFFF),
        AccentSoft = Color(0xFF7B62D2).copy(alpha = 0.10f),
        Success = Color(0xFF3F8F6A),
        Danger = Color(0xFFCF3F3C),
        Amber = Color(0xFFD97706),
        BubbleUser = Color(0xFFF1EDF7),
        TermBg = Color(0xFF29232E),
        TermFg = Color(0xFFE5DEEE),
        TermDim = Color(0xFFA39A9C),
        CodeBg = Color(0xFFF3ECDF),
        Ring = Color(0xFF7B62D2),
        AmberInk = Color(0xFF9A6400),
        SuccessInk = Color(0xFF356B52),
        SuccessSoft = Color(0xFF3F8F6A).copy(alpha = 0.10f),
        DangerSoft = Color(0xFFCF3F3C).copy(alpha = 0.08f),
        AmberSoft = Color(0xFFD97706).copy(alpha = 0.12f),
        Ripple = Color(0x142B2630),
        GradHero = listOf(Color(0xFFF2EDFA), Color(0xFFFAF6EF)),
    )

    // ===== C · 陶土助理 =====

    /** C + 亮色 */
    val CLight = LxColorSet(
        Bg = Color(0xFFFFFDF9),
        BgSide = Color(0xFFFAF5EC),
        Surface = Color(0xFFFFFFFF),
        Fg = Color(0xFF292420),
        FgMuted = Color(0xFF83776A),
        FgFaint = Color(0xFFA39685),
        Border = Color(0xFFF0E8DC),
        BorderStrong = Color(0xFFE8DFCF),
        BorderSoft = Color(0xFFF8F2E7),
        Accent = Color(0xFFB25836),
        AccentInk = Color(0xFFFFFAF6),
        AccentSoft = Color(0xFFB25836).copy(alpha = 0.08f),
        Success = Color(0xFF10A37F),
        Danger = Color(0xFFCF3F3C),
        Amber = Color(0xFFD97706),
        BubbleUser = Color(0xFFF6EFE3),
        TermBg = Color(0xFF26201A),
        TermFg = Color(0xFFE8DFCF),
        TermDim = Color(0xFFA39685),
        CodeBg = Color(0xFFF8F2E7),
        Ring = Color(0xFFB25836),
        AmberInk = Color(0xFF9A6400),
        SuccessInk = Color(0xFF0F7A5C),
        SuccessSoft = Color(0xFF10A37F).copy(alpha = 0.10f),
        DangerSoft = Color(0xFFCF3F3C).copy(alpha = 0.08f),
        AmberSoft = Color(0xFFD97706).copy(alpha = 0.12f),
        Ripple = Color(0x14292420),
        GradHero = listOf(Color(0xFFFFF8EC), Color(0xFFFFFDF9)),
    )

    /** C + 暗色 */
    val CDark = LxColorSet(
        Bg = Color(0xFF191512),
        BgSide = Color(0xFF14110E),
        Surface = Color(0xFF221D17),
        Fg = Color(0xFFEDE4D7),
        FgMuted = Color(0xFFA8988A),
        FgFaint = Color(0xFF857768),
        Border = Color(0xFF2E2821),
        BorderStrong = Color(0xFF39312A),
        BorderSoft = Color(0xFF27211A),
        Accent = Color(0xFFE0875F),
        AccentInk = Color(0xFF241009),
        AccentSoft = Color(0xFFE0875F).copy(alpha = 0.14f),
        Success = Color(0xFF4CAE83),
        Danger = Color(0xFFEF5350),
        Amber = Color(0xFFE0A23C),
        BubbleUser = Color(0xFF27211A),
        TermBg = Color(0xFF14110E),
        TermFg = Color(0xFFE0D5C2),
        TermDim = Color(0xFF857768),
        CodeBg = Color(0xFF14110E),
        Ring = Color(0xFFE0875F),
        AmberInk = Color(0xFFE0A23C),
        SuccessInk = Color(0xFF4CAE83),
        SuccessSoft = Color(0xFF4CAE83).copy(alpha = 0.16f),
        DangerSoft = Color(0xFFEF5350).copy(alpha = 0.14f),
        AmberSoft = Color(0xFFE0A23C).copy(alpha = 0.16f),
        Ripple = Color(0x1AEDE4D7),
        GradHero = listOf(Color(0xFF2B2118), Color(0xFF191512)),
    )

    /** C + 暖色（C 的本体形态） */
    val CWarm = LxColorSet(
        Bg = Color(0xFFFAF6EF),
        BgSide = Color(0xFFF3EDE2),
        Surface = Color(0xFFFFFDF9),
        Fg = Color(0xFF2C2620),
        FgMuted = Color(0xFF8A7D6F),
        FgFaint = Color(0xFFA89A8A),
        Border = Color(0xFFEAE1D3),
        BorderStrong = Color(0xFFE1D7C6),
        BorderSoft = Color(0xFFF3ECDF),
        Accent = Color(0xFFC2603E),
        AccentInk = Color(0xFFFFFAF6),
        AccentSoft = Color(0xFFC2603E).copy(alpha = 0.10f),
        Success = Color(0xFF3F8F6A),
        Danger = Color(0xFFCF3F3C),
        Amber = Color(0xFFD97706),
        BubbleUser = Color(0xFFF3ECDF),
        TermBg = Color(0xFF2B251F),
        TermFg = Color(0xFFE8DFCF),
        TermDim = Color(0xFFA89A8A),
        CodeBg = Color(0xFFF3ECDF),
        Ring = Color(0xFFC2603E),
        AmberInk = Color(0xFF9A6400),
        SuccessInk = Color(0xFF356B52),
        SuccessSoft = Color(0xFF3F8F6A).copy(alpha = 0.10f),
        DangerSoft = Color(0xFFCF3F3C).copy(alpha = 0.08f),
        AmberSoft = Color(0xFFD97706).copy(alpha = 0.12f),
        Ripple = Color(0x142C2620),
        GradHero = listOf(Color(0xFFFDF3E7), Color(0xFFFAF6EF)),
    )

    /** 按 (variant, temp) 取实例（穷举映射，新增枚举值编译器强制补这里）。 */
    fun of(variant: LxVariant, temp: LxTemp): LxColorSet = when (variant) {
        LxVariant.A -> when (temp) {
            LxTemp.Light -> ALight
            LxTemp.Dark -> ADark
            LxTemp.Warm -> AWarm
        }
        LxVariant.B -> when (temp) {
            LxTemp.Light -> BLight
            LxTemp.Dark -> BDark
            LxTemp.Warm -> BWarm
        }
        LxVariant.C -> when (temp) {
            LxTemp.Light -> CLight
            LxTemp.Dark -> CDark
            LxTemp.Warm -> CWarm
        }
    }
}

/**
 * 旧色板 object —— 已被 [LxColorSet] 数据类取代。页面层直引 `LxColors.*` 的代码已
 * 全部迁到 `Lx.colors.*`（走 CompositionLocal 才换得了主题）；这里是类型层面的
 * 兼容别名（外部如测试/预览工具还有 import 时不断链），新代码禁止再引用。
 */
@Deprecated(
    message = "改用 LxColorSet / LxColorSets（或组件里用 Lx.colors.<token>）",
    replaceWith = ReplaceWith("LxColorSet"),
)
typealias LxColors = LxColorSet
