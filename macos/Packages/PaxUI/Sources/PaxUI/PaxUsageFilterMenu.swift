import AppKit

/// 用量趋势卡的筛选值：都为 nil 是「全部」，只有 profileID 是整个 Profile，两者都有是 Profile 下的某个模型。
public struct PaxUsageFilter: Equatable, Sendable {
  public var profileID: String?
  public var modelID: String?
  public init(profileID: String? = nil, modelID: String? = nil) {
    self.profileID = profileID
    self.modelID = profileID == nil ? nil : modelID
  }
  public static let all = PaxUsageFilter()
}

/// 一个 Profile 及其下可选模型。
public struct PaxUsageFilterGroup: Equatable {
  public struct Model: Equatable {
    public var id: String
    public var name: String
    public init(id: String, name: String) {
      self.id = id
      self.name = name
    }
  }
  public var id: String
  public var name: String
  public var models: [Model]
  public init(id: String, name: String, models: [Model] = []) {
    self.id = id
    self.name = name
    self.models = models
  }
}

/// 「全部 / Profile / Profile 下的模型」分组菜单：两张趋势卡共用。
@MainActor
public enum PaxUsageFilterMenu {
  private nonisolated static let allTitle = "全部"

  /// 下拉按钮宽度：随标题文字，限制在 112–240pt。
  @MainActor public static func pickerWidth(for picker: PaxRoutePicker) -> CGFloat {
    let title = (picker.displayTitle ?? "") as NSString
    let text = title.size(withAttributes: [.font: picker.font ?? PaxTheme.Typography.caption()])
    return min(240, max(112, ceil(text.width) + PaxTheme.Routes.controlInset * 3 + 2))
  }

  /// 按钮上的完整显示标题：全部、Profile 名，或「Profile 名 · 模型名」；选择不在分组内时回退为 id。
  public nonisolated static func displayTitle(
    _ selection: PaxUsageFilter, groups: [PaxUsageFilterGroup]
  ) -> String {
    guard let profileID = selection.profileID else { return allTitle }
    let group = groups.first { $0.id == profileID }
    let profile = group?.name ?? profileID
    guard let modelID = selection.modelID else { return profile }
    return profile + " · " + (group?.models.first { $0.id == modelID }?.name ?? modelID)
  }

  /// 菜单内容不变则不重建，再选中并更新显示标题与 toolTip。
  public static func populate(
    _ picker: PaxRoutePicker, groups: [PaxUsageFilterGroup], selection: PaxUsageFilter
  ) {
    var rows = [(title: allTitle, level: 0, value: PaxUsageFilter.all)]
    for group in groups {
      rows.append((group.name, 0, PaxUsageFilter(profileID: group.id)))
      for model in group.models {
        rows.append((model.name, 1, PaxUsageFilter(profileID: group.id, modelID: model.id)))
      }
    }
    let unchanged =
      picker.numberOfItems == rows.count
      && zip(picker.itemArray, rows).allSatisfy { item, row in
        item.title == row.title && item.indentationLevel == row.level
          && (item.representedObject as? PaxUsageFilter) == row.value
      }
    if !unchanged {
      picker.removeAllItems()
      for row in rows {
        let item = NSMenuItem(title: row.title, action: nil, keyEquivalent: "")
        item.indentationLevel = row.level
        item.representedObject = row.value
        picker.menu?.addItem(item)
      }
    }
    let index = rows.firstIndex { $0.value == selection } ?? 0
    if picker.indexOfSelectedItem != index { picker.selectItem(at: index) }
    let title = displayTitle(selection, groups: groups)
    picker.displayTitle = title
    picker.toolTip = title
  }

  /// 用户在菜单里选中的值。
  public static func selection(of picker: PaxRoutePicker) -> PaxUsageFilter? {
    picker.selectedItem?.representedObject as? PaxUsageFilter
  }
}
