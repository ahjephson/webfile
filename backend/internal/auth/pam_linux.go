//go:build linux && cgo && pam

package auth

/*
#cgo LDFLAGS: -lpam
#include <security/pam_appl.h>
#include <stdlib.h>
#include <string.h>

static void free_responses(struct pam_response *resp, int n) {
    if (resp == NULL) return;
    for (int i = 0; i < n; i++) {
        free(resp[i].resp);
    }
    free(resp);
}

static int webfile_conversation(int num_msg, const struct pam_message **msg,
                                struct pam_response **response, void *appdata_ptr) {
    if (num_msg <= 0 || msg == NULL || response == NULL) return PAM_CONV_ERR;
    const char *password = (const char *)appdata_ptr;
    struct pam_response *replies = calloc((size_t)num_msg, sizeof(struct pam_response));
    if (replies == NULL) return PAM_BUF_ERR;

    for (int i = 0; i < num_msg; i++) {
        switch (msg[i]->msg_style) {
        case PAM_PROMPT_ECHO_OFF:
            replies[i].resp = strdup(password != NULL ? password : "");
            if (replies[i].resp == NULL) {
                free_responses(replies, num_msg);
                return PAM_BUF_ERR;
            }
            break;
        case PAM_PROMPT_ECHO_ON:
            replies[i].resp = strdup("");
            if (replies[i].resp == NULL) {
                free_responses(replies, num_msg);
                return PAM_BUF_ERR;
            }
            break;
        case PAM_ERROR_MSG:
        case PAM_TEXT_INFO:
            replies[i].resp = NULL;
            break;
        default:
            free_responses(replies, num_msg);
            return PAM_CONV_ERR;
        }
    }

    *response = replies;
    return PAM_SUCCESS;
}

static int webfile_pam_authenticate(const char *service, const char *username,
                                    const char *password, char **error_text) {
    struct pam_conv conv = { webfile_conversation, (void *)password };
    pam_handle_t *pamh = NULL;
    int code = pam_start(service, username, &conv, &pamh);
    if (code == PAM_SUCCESS) code = pam_authenticate(pamh, 0);
    if (code == PAM_SUCCESS) code = pam_acct_mgmt(pamh, 0);

    if (error_text != NULL) {
        const char *message = pamh != NULL ? pam_strerror(pamh, code) : "PAM initialization failed";
        *error_text = strdup(message != NULL ? message : "PAM authentication failed");
    }
    if (pamh != NULL) pam_end(pamh, code);
    return code;
}
*/
import "C"

import (
	"context"
	"errors"
	"unsafe"
)

func PAMAvailable() bool { return true }

func AuthenticatePAM(ctx context.Context, service, username, password string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cService := C.CString(service)
	cUsername := C.CString(username)
	cPassword := C.CString(password)
	defer C.free(unsafe.Pointer(cService))
	defer C.free(unsafe.Pointer(cUsername))
	defer C.free(unsafe.Pointer(cPassword))

	var cError *C.char
	code := C.webfile_pam_authenticate(cService, cUsername, cPassword, &cError)
	if cError != nil {
		defer C.free(unsafe.Pointer(cError))
	}
	if code == C.PAM_SUCCESS {
		return nil
	}
	message := "PAM authentication failed"
	if cError != nil {
		message = C.GoString(cError)
	}
	return errors.New(message)
}
