import PaxCore

extension LogsWorkbenchStore {
  var liveMessage: String {
    get { logReadMessage.original }
    set { logReadMessage = RetainedProductMessage(original: newValue) }
  }
  func setLiveMessage(_ key: String, failure: ProductFailure? = nil) {
    logReadMessage = RetainedProductMessage(key: key, failure: failure)
  }
  func liveMessageText(context: PresentationContext) -> String {
    logReadMessage.text(context: context)
  }
}
