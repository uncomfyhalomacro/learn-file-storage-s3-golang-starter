package main

import (
	"fmt"
	"net/http"
	"io"
	"time"
	"encoding/base64"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Video not found", err)
		return
	}

	user, err := cfg.db.GetUser(userID)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "User not found", err)
		return
	}

	if video.UserID != user.ID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", err)
		return
	}

	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here
	const maxMemory = 10 << 20
	r.ParseMultipartForm(maxMemory)

	file, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error processing request", err)
		return

	}
	defer file.Close()
	contentType := header.Header.Get("Content-Type") // just assume that even an empty mime-type is valid

	data, err := io.ReadAll(file)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error reading data", err)
		return
	}

	base64EncodedString := base64.StdEncoding.EncodeToString(data)

	thumbnailUrl := fmt.Sprintf("data:%s;base64,%s", contentType, base64EncodedString)
	video.UpdatedAt = time.Now()
	video.ThumbnailURL = &thumbnailUrl

	err = cfg.db.UpdateVideo(video)

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Error updating data", err)
		return

	}

	respondWithJSON(w, http.StatusOK, video)
}
