# GeoLite发布条件核实

核实日期：2026-11-12。仅记录本次取证及发布阻断，不替代法律意见。

## 随包来源

rulesets/geoip-cn.srs固定为SagerNet/sing-geoip的rule-set提交7fe82a879ad2666526730c195b55a6d8d9147908，GitHub提交日期2026-09-12。SHA256见rulesets/manifest.json，仓库于2026-09-30取得该资源。

上游生成器[main.go](https://github.com/SagerNet/sing-geoip/blob/main/main.go)从Dreamacro/maxmind-geoip下载Country.mmdb，按RegisteredCountry.IsoCode分组，再写sing-box SRS。Dreamacro的[来源声明](https://github.com/Dreamacro/maxmind-geoip)确认包含MaxMind GeoLite2数据。格式转换不能当作来源许可已解除的证据。

## 已核实条款

- [MaxMind当前EULA](https://www.maxmind.com/en/geolite/eula) §3：可版权部分适用CC BY-SA 4.0，要求保留MaxMind署名；§6同时要求相应接收方义务、更新及删除。现有NOTICE只完成来源署名，不能据此确认全部条件已满足。
- §6原文："You shall cease use of and destroy (i) any old versions of the Services within thirty (30) days following the release of the updated GeoLite Databases"。
- [MaxMind开发者说明](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data/)明确用户应保持数据最新，并在新版发布后30天内删除旧数据库。

本次没有查到Pax针对长期随DMG、永久Git标签/旧Release分发该衍生资源的额外许可。已知内置规则超过两个月，且当前发布设计保留旧版本；未证明满足上述更新/删除条件，不能将原待办直接勾为通过。

## 处理边界

按已批准的发布流程，可继续固化流程、修导出材料、构建和验收本地候选，以及准备网站发布信息；公开源码/新附件上传和官网切换暂缓。

建议用户批准后，将中国IP规则改用许可及来源可追溯的非GeoLite数据，连同远程更新源、署名、规则命中和回归一并验证。APNIC分配记录表示注册地址/分配归属，不能直接承诺与当前地理规则等价；需要明确新语义后实施。另一条路径是取得覆盖本产品分发方式的许可并落实接收方/更新义务。

本票不默默删除规则、更换数据源，也不删除已发布0.1.0或改写既有标签。旧版本公开分发的处理同样需要用户决定。
