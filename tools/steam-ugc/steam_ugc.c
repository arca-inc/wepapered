// wepapered-steam-ugc — subscribe to and download a Wallpaper Engine workshop
// item through the running Steam client, using the Steamworks "flat" C API
// exported by libsteam_api.so (no SDK headers needed). Steam downloads the item
// into …/steamapps/workshop/content/431960/<id>/, where wepapered already looks.
//
//   wepapered-steam-ugc            → connectivity check (prints subscribed count)
//   wepapered-steam-ugc <id> [...] → subscribe + download each id, wait for install
//
// Requires: Steam running and logged in, the account owning app 431960, and
// ~/.steam/sdk64/steamclient.so present.
#include <stdint.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

typedef uint64_t PublishedFileId_t;
typedef uint64_t SteamAPICall_t;

// ESteamAPIInitResult: 0 == k_ESteamAPIInitResult_OK
extern int   SteamAPI_InitFlat(char *pOutErrMsgUTF8 /* >=1024 */);
extern void  SteamAPI_Shutdown(void);
extern void  SteamAPI_RunCallbacks(void);
extern void *SteamAPI_SteamUGC_v021(void);
extern SteamAPICall_t SteamAPI_ISteamUGC_SubscribeItem(void *self, PublishedFileId_t id);
extern bool     SteamAPI_ISteamUGC_DownloadItem(void *self, PublishedFileId_t id, bool bHighPriority);
extern uint32_t SteamAPI_ISteamUGC_GetItemState(void *self, PublishedFileId_t id);
extern uint32_t SteamAPI_ISteamUGC_GetNumSubscribedItems(void *self);

enum {
    ItemSubscribed     = 1,
    ItemInstalled      = 4,
    ItemNeedsUpdate    = 8,
    ItemDownloading    = 16,
    ItemDownloadPending = 32,
};

int main(int argc, char **argv) {
    setenv("SteamAppId", "431960", 1);
    setenv("SteamGameId", "431960", 1);

    char err[1024] = {0};
    int r = SteamAPI_InitFlat(err);
    if (r != 0) {
        fprintf(stderr, "SteamAPI_InitFlat failed (%d): %s\n", r, err);
        return 1;
    }
    void *ugc = SteamAPI_SteamUGC_v021();
    if (!ugc) { fprintf(stderr, "ISteamUGC unavailable\n"); SteamAPI_Shutdown(); return 1; }

    if (argc < 2) {
        printf("OK: connected, %u subscribed items\n", SteamAPI_ISteamUGC_GetNumSubscribedItems(ugc));
        SteamAPI_Shutdown();
        return 0;
    }

    int rc = 0;
    for (int a = 1; a < argc; a++) {
        PublishedFileId_t id = strtoull(argv[a], NULL, 10);
        if (!id) { fprintf(stderr, "bad id %s\n", argv[a]); rc = 2; continue; }
        SteamAPI_ISteamUGC_SubscribeItem(ugc, id);
        SteamAPI_ISteamUGC_DownloadItem(ugc, id, true);
        int done = 0;
        for (int i = 0; i < 3000; i++) { // up to ~10 min
            SteamAPI_RunCallbacks();
            uint32_t st = SteamAPI_ISteamUGC_GetItemState(ugc, id);
            if ((st & ItemInstalled) &&
                !(st & (ItemNeedsUpdate | ItemDownloading | ItemDownloadPending))) {
                printf("installed %llu\n", (unsigned long long)id);
                fflush(stdout);
                done = 1;
                break;
            }
            usleep(200000); // 200 ms
        }
        if (!done) { fprintf(stderr, "timeout for %llu\n", (unsigned long long)id); rc = 1; }
    }
    SteamAPI_Shutdown();
    return rc;
}
