/*******************************************************************************
 * Copyright (c) 2019 IBM Corporation and others.
 * All rights reserved. This program and the accompanying materials
 * are made available under the terms of the Eclipse Public License v2.0
 * which accompanies this distribution, and is available at
 * http://www.eclipse.org/legal/epl-v20.html
 *
 * Contributors:
 *     IBM Corporation - initial API and implementation
 *******************************************************************************/

package codewind

import (
	"errors"
	"net/http"

	"github.com/eclipse/codewind-operator/pkg/security"
	"github.com/eclipse/codewind-operator/pkg/util"
)

// AddCodewindToKeycloak : sets up Keycloak with a realm, client and user
// Returns a clientKey or an error
func AddCodewindToKeycloak(workspaceID string, authURL string, realmName string, keycloakAdminUser string, keycloakAdminPass string, gatekeeperPublicURL string, devUsername string, clientName string) (string, error) {

	var keycloakConfig security.KeycloakConfiguration
	keycloakConfig.RealmName = realmName
	keycloakConfig.AuthURL = authURL
	keycloakConfig.WorkspaceID = workspaceID
	keycloakConfig.KeycloakAdminPassword = keycloakAdminPass
	keycloakConfig.KeycloakAdminUsername = keycloakAdminUser
	keycloakConfig.DevUsername = devUsername
	keycloakConfig.GatekeeperPublicURL = gatekeeperPublicURL
	keycloakConfig.ClientName = clientName

	// Wait for the Keycloak service to respond
	log.Info("Waiting for Keycloak to start", "URL", keycloakConfig.AuthURL)
	startErr := util.WaitForService(keycloakConfig.AuthURL, 200, 500)
	if startErr != nil {
		return "", errors.New("Keycloak did not start in a reasonable about of time")
	}

	log.Info("Configuring Keycloak...")
	log.Info(keycloakConfig.AuthURL)

	tokens, secErr := security.SecAuthenticate(http.DefaultClient, &keycloakConfig)
	if secErr != nil {
		return "", secErr.Err
	}

	secErr = configureKeycloakRealm(http.DefaultClient, &keycloakConfig, tokens.AccessToken)
	if secErr != nil {
		return "", secErr.Err
	}

	secErr = configureKeycloakClient(http.DefaultClient, &keycloakConfig, tokens.AccessToken)
	if secErr != nil {
		return "", secErr.Err
	}

	secErr = configureKeycloakAccessRole(http.DefaultClient, &keycloakConfig, tokens.AccessToken, "codewind-"+keycloakConfig.WorkspaceID)
	if secErr != nil {
		return "", secErr.Err
	}

	secErr = configureKeycloakUser(http.DefaultClient, &keycloakConfig, tokens.AccessToken)
	if secErr != nil {
		return "", secErr.Err
	}

	secErr = grantUserAccessToDeployment(http.DefaultClient, &keycloakConfig, tokens.AccessToken)
	if secErr != nil {
		return "", secErr.Err
	}

	registeredSecret, secErr := fetchClientSecret(http.DefaultClient, &keycloakConfig, tokens.AccessToken)
	if secErr != nil {
		return "", secErr.Err
	}

	return registeredSecret.Secret, nil

}

func configureKeycloakRealm(httpClient util.HTTPClient, keycloakConfig *security.KeycloakConfiguration, accessToken string) *security.SecError {
	// Check if realm is already registered
	realm, _ := security.SecRealmGet(httpClient, keycloakConfig, accessToken)
	if realm != nil && realm.ID != "" {
		log.Info("Updating existing Keycloak realm ", "name", realm.DisplayName)
	} else {
		// Create a new realm
		log.Info("Creating new Keycloak realm")
		secErr := security.SecRealmCreate(httpClient, keycloakConfig, accessToken)
		if secErr != nil {
			return secErr
		}
	}
	return nil
}

func configureKeycloakClient(httpClient util.HTTPClient, keycloakConfig *security.KeycloakConfiguration, accessToken string) *security.SecError {
	// Check if the client is already registered
	log.Info("Checking for Keycloak client", "name", keycloakConfig.ClientName)
	registeredClient, _ := security.SecClientGet(httpClient, keycloakConfig, accessToken)
	if registeredClient != nil && registeredClient.ID != "" {
		log.Info("Updating existing Keycloak client '", "name", registeredClient.Name)
		secErr := security.SecClientAppendURL(httpClient, keycloakConfig, accessToken)
		if secErr != nil {
			return secErr
		}
	} else {
		// Create a new client
		log.Info("Creating Keycloak client")
		secErr := security.SecClientCreate(httpClient, keycloakConfig, accessToken, keycloakConfig.GatekeeperPublicURL+"/*")
		if secErr != nil {
			return secErr
		}
	}
	return nil
}

func configureKeycloakAccessRole(httpClient util.HTTPClient, keycloakConfig *security.KeycloakConfiguration, accessToken string, accessRoleName string) *security.SecError {
	// Create a new access role for this deployment
	log.Info("Creating access role in realm", "rolename", accessRoleName, "realmName", keycloakConfig.RealmName)
	secErr, httpStatusCode := security.SecRoleCreate(httpClient, keycloakConfig, accessToken, accessRoleName)
	if httpStatusCode == http.StatusConflict {
		return nil
	}
	if secErr != nil {
		log.Error(secErr.Err, "Access role create failed", secErr.Desc)
		return secErr
	}
	return nil
}

//Check if the user exists and is registered
func configureKeycloakUser(httpClient util.HTTPClient, keycloakConfig *security.KeycloakConfiguration, accessToken string) *security.SecError {
	registeredUser, secErr := security.SecUserGet(httpClient, keycloakConfig, accessToken)
	if secErr == nil && registeredUser != nil {
		return nil
	}
	log.Error(secErr.Err, "Configuring user failed", secErr.Desc)
	return secErr
}

// Grant the user access to this Deployment
func grantUserAccessToDeployment(httpClient util.HTTPClient, keycloakConfig *security.KeycloakConfiguration, accessToken string) *security.SecError {
	log.Info("Grant access to deployment", "Username", keycloakConfig.DevUsername, "Workspace", keycloakConfig.WorkspaceID)
	secErr := security.SecUserAddRole(httpClient, keycloakConfig, accessToken, "codewind-"+keycloakConfig.WorkspaceID)
	if secErr != nil {
		log.Error(secErr.Err, "Granting access to deployment", "")
		return secErr
	}
	return nil
}

// // fetchClientSecret : Load client secret
func fetchClientSecret(httpClient util.HTTPClient, keycloakConfig *security.KeycloakConfiguration, accessToken string) (*security.RegisteredClientSecret, *security.SecError) {
	secretName := "codewind-" + keycloakConfig.WorkspaceID
	log.Info("Fetching client secret", "name", secretName)
	registeredSecret, secErr := security.SecClientGetSecret(httpClient, keycloakConfig, accessToken)
	if secErr != nil {
		log.Error(secErr.Err, "Error fetching client secret ", "name", secretName)
		return nil, secErr
	}
	return registeredSecret, nil
}
