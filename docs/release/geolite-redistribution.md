# GeoLite发布条件核实

核实日期：2026-11-12。仅记录本次取证及待核实条件，不替代法律意见。

## 随包来源

rulesets/geoip-cn.srs固定为SagerNet/sing-geoip的rule-set提交7fe82a879ad2666526730c195b55a6d8d9147908，GitHub提交日期2026-09-12。SHA256见rulesets/manifest.json，仓库于2026-09-30取得该资源。

上游生成器[main.go](https://github.com/SagerNet/sing-geoip/blob/main/main.go)从Dreamacro/maxmind-geoip下载Country.mmdb，按RegisteredCountry.IsoCode分组，再写sing-box SRS。Dreamacro的[来源声明](https://github.com/Dreamacro/maxmind-geoip)确认包含MaxMind GeoLite2数据。格式转换不能当作来源许可已解除的证据。

## 已核实条款

- [MaxMind当前EULA](https://www.maxmind.com/en/geolite/eula) §3：可版权部分适用CC BY-SA 4.0，要求保留MaxMind署名；§6同时要求相应接收方义务、更新及删除。现有NOTICE只完成来源署名，不能据此确认全部条件已满足。
- §6原文："You shall cease use of and destroy (i) any old versions of the Services within thirty (30) days following the release of the updated GeoLite Databases"。
- [MaxMind开发者说明](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data/)明确用户应保持数据最新，并在新版发布后30天内删除旧数据库。

本次没有查到Pax针对长期随DMG、永久Git标签/旧Release分发该衍生资源的额外许可。已知内置规则超过两个月，且当前发布设计保留旧版本；未证明满足上述更新/删除条件，不能将原待办直接勾为通过。

## 当前发布决定

2026-11-12用户明确要求保留现有地址表，并将公开源码、安装包与官网下载更新为最新版。该指令取代此前暂停0.1.1公开发布的安排；继续发布不等于上述条款已核实满足。

0.1.1保留rulesets/geoip-cn.srs、对应更新地址和现有分流行为，不更换或删除规则，不改写旧标签和旧安装包。来源署名与本次取证记录保留。用户后续如要求处理数据来源，再单独评估许可与分流语义。
