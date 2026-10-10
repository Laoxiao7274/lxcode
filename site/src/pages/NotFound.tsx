// 404 页：哈希路由没有匹配到路径时渲染（不静默回首页，避免「链接错了却像没事」）。

export default function NotFound({ path }: { path: string }) {
  return (
    <section className="sec">
      <div className="wrap">
        <div className="sec-head">
          <p className="eyebrow">404</p>
          <h1 style={{ fontSize: "var(--fs-h1)" }}>没有这个页面</h1>
          <p className="sec-lede">
            路径 <span className="mono">{path}</span> 不在站点里。
          </p>
        </div>
        <p className="row-actions">
          <a className="btn btn-p" href="#/">
            返回首页
          </a>
          <a className="btn" href="#/download">
            去下载页
          </a>
        </p>
      </div>
    </section>
  );
}
