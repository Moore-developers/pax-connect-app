# 内置分流规则集（T3 本机开发）

资源在2026-09-30经GitHub内容API获取，启动离线使用，manifest记录SHA-256。

- geosite-cn：SagerNet/sing-geosite，rule-set提交 `417ea33286a8c3a77fdce9b68ce25d58565dad43`；SHA-256 `2f4af0be08f2ff494a8fcdef992dc92a2267600ce26c9bba17421d1ce9a6715c`。原始域名数据v2fly/domain-list-community，MIT；生成器GPL-3.0-or-later。旧来源不明文件归档。
- geoip-cn：SagerNet/sing-geoip，rule-set提交 `7fe82a879ad2666526730c195b55a6d8d9147908`；SHA-256 `ebee603fdf402314b44b9f653cdcf6d9cc9c41e84e2b3515e3123c5c920a93bc`，与S0逐字节一致。生成器GPL-3.0-or-later；原始数据来自Dreamacro/maxmind-geoip转供的MaxMind GeoLite2。

生成器声明及域名MIT许可证保存在third_party/licenses；GeoLite数据来源署名见同目录NOTICE。生成器许可不替代数据许可。本包仅用于本机开发/自用，尚未承诺满足向他人分发GeoLite衍生数据的全部条款；对外分发前核实GeoLite EULA、更新与署名要求。远程更新按Geo来源合同执行。

来源：https://github.com/SagerNet/sing-geosite ，https://github.com/SagerNet/sing-geoip ，https://github.com/v2fly/domain-list-community ，https://github.com/Dreamacro/maxmind-geoip ，https://dev.maxmind.com/geoip/geolite2-free-geolocation-data/ 。

Google规则：2026-11-02来自SagerNet/sing-geosite rule-set提交`5cd46a0648352abddcabf7e2d300ae2ed07af09a`的geosite-google.srs；SHA-256 `992d7ba91ce9c6e1c248d9ef0d58b2a17281415526b6a560dce6368e4f72e1f2`。同样由社区域名数据生成，并非Google官方清单，现有域名许可证适用。

广告拦截：`adblock-lite.srs` 为 217heidai/adblockfilters 的 2026-11-06 Release 原样资源（46,960 字节），SHA-256 见 manifest。固定更新地址为 https://github.com/217heidai/adblockfilters/releases/download/rule/adblocksingboxlite.srs 。本次解码得到一个匹配组、5,898 个域名后缀；数量只描述随包基线，不代表后续更新。项目声明 GPL v3，许可与来源见 `third_party/licenses/adblockfilters-NOTICE.txt` 和已有 GPL-3.0 全文。
