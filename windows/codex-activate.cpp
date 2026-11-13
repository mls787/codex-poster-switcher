#include <windows.h>
#include <shobjidl.h>
#include <iostream>

// Activate a registered Windows app with command-line arguments. This small
// helper uses the documented IApplicationActivationManager interface.
int wmain(int argc, wchar_t* argv[]) {
    if (argc != 3) {
        std::wcerr << L"usage: codex-activate.exe <AppUserModelID> <arguments>\n";
        return 2;
    }

    HRESULT hr = CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);
    if (FAILED(hr)) {
        std::wcerr << L"COM initialization failed: " << std::hex << hr << L"\n";
        return 3;
    }

    IApplicationActivationManager* manager = nullptr;
    hr = CoCreateInstance(CLSID_ApplicationActivationManager, nullptr,
                          CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&manager));
    if (FAILED(hr)) {
        std::wcerr << L"Activation manager unavailable: " << std::hex << hr << L"\n";
        CoUninitialize();
        return 4;
    }

    DWORD processId = 0;
    hr = manager->ActivateApplication(argv[1], argv[2], AO_NONE, &processId);
    manager->Release();
    CoUninitialize();
    if (FAILED(hr)) {
        std::wcerr << L"Codex activation failed: " << std::hex << hr << L"\n";
        return 5;
    }
    return 0;
}
