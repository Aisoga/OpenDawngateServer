// dgsim.dll is installed as PhysXLoader.dll in the server working copy: Dawngate.exe imports it
// statically, so the loader maps our DLL on the primary thread before the game's main() runs.
// Every export of the game's PhysXLoader.dll (PhysX 2.8.4, ten exports, ordinals 1-10) is
// forwarded to the original, which setup_server_copy.py renames to PhysXLoader_orig.dll.
#pragma comment(linker, "/export:NxCreateModule=PhysXLoader_orig.NxCreateModule,@1")
#pragma comment(linker, "/export:NxCreatePhysicsSDK=PhysXLoader_orig.NxCreatePhysicsSDK,@2")
#pragma comment(linker, "/export:NxCreatePhysicsSDKWithID=PhysXLoader_orig.NxCreatePhysicsSDKWithID,@3")
#pragma comment(linker, "/export:NxGetCookingLib=PhysXLoader_orig.NxGetCookingLib,@4")
#pragma comment(linker, "/export:NxGetCookingLibWithID=PhysXLoader_orig.NxGetCookingLibWithID,@5")
#pragma comment(linker, "/export:NxGetFoundationSDK=PhysXLoader_orig.NxGetFoundationSDK,@6")
#pragma comment(linker, "/export:NxGetPhysicsSDK=PhysXLoader_orig.NxGetPhysicsSDK,@7")
#pragma comment(linker, "/export:NxGetPhysicsSDKAllocator=PhysXLoader_orig.NxGetPhysicsSDKAllocator,@8")
#pragma comment(linker, "/export:NxGetUtilLib=PhysXLoader_orig.NxGetUtilLib,@9")
#pragma comment(linker, "/export:NxReleasePhysicsSDK=PhysXLoader_orig.NxReleasePhysicsSDK,@10")
