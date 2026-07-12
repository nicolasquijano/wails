// wails-cef-helper is intentionally a C++-only CEF subprocess executable.
// Chromium's Linux zygote may fork renderer/GPU/utility processes after it is
// started, so this binary must never link or load the Go runtime.

#include "include/cef_app.h"
#include "include/cef_command_line.h"
#include "include/cef_frame.h"
#include "include/cef_parser.h"
#include "include/cef_process_message.h"
#include "include/cef_v8.h"
#include "include/wrapper/cef_helpers.h"

#include <string>

namespace {

constexpr char kExtensionName[] = "wails.cef";
constexpr char kInvokeMessage[] = "wails.invoke.async";
constexpr char kResultMessage[] = "wails.invoke.result";

// Kept deliberately small: the Wails runtime switches to its async transport
// when window.wails.invokeAsync exists. The result is delivered through the
// existing Android-compatible callback used by the Go single-process bridge.
constexpr char kExtensionCode[] = R"JS(
(function() {
  native function wails_invokeAsync(callId, payload);
  function install() {
    if (!window.wails) window.wails = {};
    window.wails.invokeAsync = function(callId, payload) {
      if (typeof callId !== 'string' || !callId) return;
      if (typeof payload !== 'string') {
        try { payload = JSON.stringify(payload); } catch (_) { return; }
      }
      wails_invokeAsync(callId, payload);
    };
  }
  install();
  // runtime.js may replace window.wails during module initialization.
  document.addEventListener('DOMContentLoaded', install, { once: true });
})();
)JS";

class InvokeHandler final : public CefV8Handler {
 public:
  bool Execute(const CefString& name,
               CefRefPtr<CefV8Value>,
               const CefV8ValueList& arguments,
               CefRefPtr<CefV8Value>&,
               CefString& exception) override {
    CEF_REQUIRE_RENDERER_THREAD();
    if (name != "wails_invokeAsync" || arguments.size() != 2 ||
        !arguments[0]->IsString() || !arguments[1]->IsString()) {
      exception = "wails/cef: invalid async invocation";
      return true;
    }
    const auto context = CefV8Context::GetCurrentContext();
    if (!context || !context->GetFrame()) {
      exception = "wails/cef: no active frame";
      return true;
    }
    const auto message = CefProcessMessage::Create(kInvokeMessage);
    auto args = message->GetArgumentList();
    args->SetInt(0, 1);  // protocol version
    args->SetString(1, arguments[0]->GetStringValue());
    args->SetString(2, arguments[1]->GetStringValue());
    context->GetFrame()->SendProcessMessage(PID_BROWSER, message);
    return true;
  }

 private:
  IMPLEMENT_REFCOUNTING(InvokeHandler);
};

class RenderHandler final : public CefRenderProcessHandler {
 public:
  void OnWebKitInitialized() override {
    CEF_REQUIRE_RENDERER_THREAD();
    CefRegisterExtension(kExtensionName, kExtensionCode, new InvokeHandler());
  }

  bool OnProcessMessageReceived(CefRefPtr<CefBrowser>,
                                CefRefPtr<CefFrame> frame,
                                CefProcessId source_process,
                                CefRefPtr<CefProcessMessage> message) override {
    CEF_REQUIRE_RENDERER_THREAD();
    if (source_process != PID_BROWSER || !message ||
        message->GetName() != kResultMessage || !frame) {
      return false;
    }
    auto args = message->GetArgumentList();
    if (!args || args->GetSize() != 3) return true;
    // Quote every dynamic value through CefWriteJSON to prevent response/error
    // data from becoming executable JavaScript.
    auto quote = [](const CefString& value) {
      auto v = CefValue::Create();
      v->SetString(value);
      return CefWriteJSON(v, JSON_WRITER_DEFAULT);
    };
    const std::string script = "if(window._wailsAndroidCallback){window._wailsAndroidCallback(" +
        quote(args->GetString(0)).ToString() + "," +
        quote(args->GetString(1)).ToString() + "," +
        quote(args->GetString(2)).ToString() + ");}";
    frame->ExecuteJavaScript(script, frame->GetURL(), 0);
    return true;
  }

 private:
  IMPLEMENT_REFCOUNTING(RenderHandler);
};

class HelperApp final : public CefApp {
 public:
  CefRefPtr<CefRenderProcessHandler> GetRenderProcessHandler() override {
    return new RenderHandler();
  }

 private:
  IMPLEMENT_REFCOUNTING(HelperApp);
};

}  // namespace

int main(int argc, char* argv[]) {
  CefMainArgs args(argc, argv);
  return CefExecuteProcess(args, new HelperApp(), nullptr);
}
